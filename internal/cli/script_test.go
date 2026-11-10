package cli_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/schemas"
)

const scriptDir = "../../testdata/script"

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"tfcensus": func() {
			os.Exit(cli.Run(context.Background(), os.Args, os.Stdout, os.Stderr))
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
	got := cli.Run(context.Background(), args[1:], os.Stdout, os.Stderr)
	if got != want {
		_, _ = fmt.Fprintf(os.Stderr, "expect-exit: tfcensus exited %d, want %d\n", got, want)
		return 1
	}
	return 0
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
