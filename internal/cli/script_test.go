package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
	"github.com/sassdavid/tfcensus/internal/testutil/vcr"
	"github.com/sassdavid/tfcensus/schemas"
)

const (
	scriptDir     = "../../testdata/script"
	cassetteDir   = "../../testdata/cassettes"
	driftFixtures = "../../testdata/drift"
)

// Test-only environment read by the tfcensus command of the test binary.
const (
	// envCassette replays every HTTP request from this cassette.
	envCassette = "TEST_CASSETTE"
	// envNow fixes the clock (RFC 3339), so JSON envelopes and probe timestamps are stable.
	envNow = "TEST_NOW"
)

func TestMain(m *testing.M) {
	fakebin.Serve() // units_test.go runs a fake terragrunt
	testscript.Main(m, map[string]func(){
		"tfcensus": func() {
			os.Exit(runTfcensus(os.Args[1:]))
		},
		// expect-exit <code> tfcensus [args...] succeeds only when tfcensus exits with <code>.
		"expect-exit": func() {
			os.Exit(expectExit(os.Args[1:]))
		},
	})
}

func expectExit(args []string) int {
	const minArgs = 2
	if len(args) < minArgs || args[1] != "tfcensus" {
		_, _ = fmt.Fprintln(os.Stderr, "usage: expect-exit <code> tfcensus [args...]")
		return 1
	}
	want, err := strconv.Atoi(args[0])
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "expect-exit: bad code %q\n", args[0])
		return 1
	}
	got := runTfcensus(args[2:])
	if got != want {
		_, _ = fmt.Fprintf(os.Stderr, "expect-exit: tfcensus exited %d, want %d\n", got, want)
		return 1
	}
	return 0
}

// runTfcensus runs the CLI the way main does, with the test hooks from the environment. AWS
// cassettes are written from API reference shapes, not recorded, so they are only replayed.
func runTfcensus(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	d := &app.Deps{
		Stdin:  os.Stdin,
		Stdout: os.Stdout,
		Stderr: os.Stderr,
		Env:    app.Env(os.Environ()),
		Clock:  time.Now,
		Getwd:  os.Getwd,
	}
	if now := os.Getenv(envNow); now != "" {
		t, err := time.Parse(time.RFC3339, now)
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "%s: %v\n", envNow, err)
			return 1
		}
		d.Clock = func() time.Time { return t }
	}
	if path := os.Getenv(envCassette); path != "" {
		rec, err := vcr.New(path, vcr.Options{Scrubber: awstest.Scrubber()})
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return 1
		}
		d.HTTPTransport = rec
		defer func() { _ = rec.Stop() }()
	}
	return cli.Execute(ctx, d, args)
}

func scriptParams(t *testing.T, files ...string) testscript.Params {
	t.Helper()
	return testscript.Params{
		Files:               files,
		UpdateScripts:       testutil.Updating(),
		RequireExplicitExec: true,
		RequireUniqueNames:  true,
		Setup: func(env *testscript.Env) error {
			env.Setenv("HOME", filepath.Join(env.WorkDir, "home"))
			env.Setenv("XDG_CONFIG_HOME", filepath.Join(env.WorkDir, ".config"))
			env.Setenv("XDG_CACHE_HOME", filepath.Join(env.WorkDir, ".cache"))
			env.Setenv("NO_COLOR", "1")
			// A -race binary sleeps 1s before exiting by default.
			env.Setenv("GORACE", "atexit_sleep_ms=0")
			// Nothing may reach AWS: no instance metadata, and no profile from the machine.
			env.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			for _, k := range []string{
				"AWS_PROFILE", "AWS_DEFAULT_PROFILE", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
				"AWS_SESSION_TOKEN", "AWS_CONFIG_FILE", "AWS_SHARED_CREDENTIALS_FILE", "AWS_REGION",
				"AWS_DEFAULT_REGION",
			} {
				env.Setenv(k, "")
			}
			// Keep only testscript's own bin directory, so a terragrunt installed on the machine
			// doesn't change units output. Scripts that need a fake terragrunt prepend its directory.
			bin, _, _ := strings.Cut(env.Getenv("PATH"), string(os.PathListSeparator))
			env.Setenv("PATH", bin)
			cassettes, err := filepath.Abs(cassetteDir)
			if err != nil {
				return err
			}
			env.Setenv("CASSETTES", cassettes)
			repos, err := filepath.Abs(reposDir)
			if err != nil {
				return err
			}
			env.Setenv("REPOS", repos)
			driftDir, err := filepath.Abs(driftFixtures)
			if err != nil {
				return err
			}
			env.Setenv("DRIFT", driftDir)
			return nil
		},
		Cmds: map[string]func(ts *testscript.TestScript, neg bool, args []string){
			"jsonschema": cmdJSONSchema,
		},
	}
}

// cmdJSONSchema validates stdout, stderr or a file against an embedded schema:
//
//	jsonschema report-v1.json stdout
func cmdJSONSchema(ts *testscript.TestScript, neg bool, args []string) {
	const wantArgs = 2
	if len(args) != wantArgs {
		ts.Fatalf("usage: jsonschema <schema-file> <stdout|stderr|file>")
	}
	// ReadFile understands "stdout" and "stderr" as the last command's output.
	err := validateJSON(args[0], []byte(ts.ReadFile(args[1])))
	switch {
	case neg && err == nil:
		ts.Fatalf("%s unexpectedly valid against %s", args[1], args[0])
	case !neg && err != nil:
		ts.Fatalf("%s invalid against %s: %v", args[1], args[0], err)
	}
}

func validateJSON(schemaName string, data []byte) error {
	raw, err := schemas.Read(schemaName)
	if err != nil {
		return err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("parse schema: %w", err)
	}
	c := jsonschema.NewCompiler()
	c.AssertFormat()
	if err = c.AddResource(schemaName, doc); err != nil {
		return fmt.Errorf("add schema: %w", err)
	}
	sch, err := c.Compile(schemaName)
	if err != nil {
		return fmt.Errorf("compile schema: %w", err)
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("parse instance: %w", err)
	}
	return sch.Validate(inst) //nolint:wrapcheck // the validation error is the message
}

// TestScripts runs every testdata/script/*.txtar that isn't bound to a spec criterion.
func TestScripts(t *testing.T) {
	t.Parallel()
	matches, err := filepath.Glob(filepath.Join(scriptDir, "*.txtar"))
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for _, m := range matches {
		if !strings.HasPrefix(filepath.Base(m), "spec_") {
			files = append(files, m)
		}
	}
	testscript.Run(t, scriptParams(t, files...))
}

// TestSpec_config_1: `config show --sources` prints key, value and source for every key (golden).
func TestSpec_config_1(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_config_1.txtar")))
}

// TestSpec_config_2: unknown keys and wrong types each produce a precise error.
func TestSpec_config_2(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_config_2.txtar")))
}

// TestSpec_accounts_1: an expired SSO token gives one clear message per profile, the run
// continues, and doctor exits 4 only when every profile fails.
func TestSpec_accounts_1(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_accounts_1.txtar")))
}

// TestSpec_accounts_2: doctor output from stubbed responses that mirror the real-world check in
// the accounts spec (golden).
func TestSpec_accounts_2(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_accounts_2.txtar")))
}

// TestSpec_accounts_3: in org mode, a member account that denies the assume-role is reported and
// the other accounts are probed.
func TestSpec_accounts_3(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_accounts_3.txtar")))
}

// TestSpec_scan_1: an end-to-end scan with stubbed AWS and fixture states covers every category
// in every format (goldens).
func TestSpec_scan_1(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_scan_1.txtar")))
}

// TestSpec_scan_3: --fail-on unmanaged --types 'AWS::IAM::*' exits 3 only for IAM findings.
func TestSpec_scan_3(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_scan_3.txtar")))
}

// TestSpec_ownership_2: an expired rule stops matching and produces a warning (injected clock).
func TestSpec_ownership_2(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_ownership_2.txtar")))
}

// TestSpec_ownership_3: the scan --explain trace (golden).
func TestSpec_ownership_3(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_ownership_3.txtar")))
}

// TestSpec_ownership_4: the rules schema is in schemas/config-v1.json, and CEL compile errors
// report the rule name and column.
func TestSpec_ownership_4(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_ownership_4.txtar")))
}

// TestSpec_drift_1: ingest mode over recorded json-out-dir plans and a run report for 12 units:
// 3 identical tag drifts collapse into 1 group and the failed unit is an error (goldens).
func TestSpec_drift_1(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_drift_1.txtar")))
}

// TestSpec_drift_2: a noise rule past its expires date stops hiding drift and warns, and
// never_hide keeps a deleted resource visible.
func TestSpec_drift_2(t *testing.T) {
	t.Parallel()
	testscript.Run(t, scriptParams(t, filepath.Join(scriptDir, "spec_drift_2.txtar")))
}
