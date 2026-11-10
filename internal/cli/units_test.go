package cli_test

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

const (
	reposDir     = "../../testdata/repos"
	recordingDir = "../../testdata/terragrunt/tg-live"
	goldenDir    = "../../testdata/golden/units"
	prodFilter   = "./live/prod/**"
)

// fakeTerragrunt installs a terragrunt that replays the recordings of tg-live. find answers from
// findFile, or from find-prod.json when called with --filter ./live/prod/**.
func fakeTerragrunt(t *testing.T, findFile string) string {
	t.Helper()
	abs := func(p string) string {
		a, err := filepath.Abs(p)
		require.NoError(t, err)
		return a
	}
	findArgs := []string{"find", "--json", "--dag", "--dependencies", "--exclude", "--no-color", "--log-level", "error"}
	rules := []fakebin.Rule{
		{
			Args:       append(append([]string{}, findArgs...), "--filter", prodFilter),
			StdoutFile: abs(filepath.Join(recordingDir, "find-prod.json")),
		},
		{Args: findArgs, StdoutFile: abs(filepath.Join(recordingDir, findFile))},
		{Args: []string{"stack", "generate"}},
	}
	renders, err := filepath.Glob(filepath.Join(recordingDir, "render", "*"))
	require.NoError(t, err)
	for _, f := range renders {
		ext := filepath.Ext(f)
		unit := strings.ReplaceAll(strings.TrimSuffix(filepath.Base(f), ext), "__", "/")
		rule := fakebin.Rule{Args: []string{"render", "--working-dir", unit}}
		if ext == ".json" {
			rule.StdoutFile = abs(f)
		} else {
			stderr, readErr := os.ReadFile(f)
			require.NoError(t, readErr)
			rule.Stderr, rule.ExitCode = string(stderr), 1
		}
		rules = append(rules, rule)
	}
	return fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: rules})
}

type cliResult struct {
	stdout, stderr string
	code           int
}

func runCLI(t *testing.T, bin string, args ...string) cliResult {
	t.Helper()
	cwd, err := filepath.Abs(reposDir)
	require.NoError(t, err)
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env: app.Env{
			"PATH=" + filepath.Dir(bin), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"NO_COLOR=1",
		},
		Clock: func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		Getwd: func() (string, error) { return cwd, nil },
	}
	code := cli.Execute(t.Context(), d, args)
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// stableJSON replaces the build-dependent version so goldens match every build.
func stableJSON(s string) []byte {
	return []byte(regexp.MustCompile(`(?m)^  "version": ".*",$`).ReplaceAllString(s, `  "version": "test",`))
}

// TestSpec_discovery_1: tg-live (root.hcl with account/region/env files, 12 units, 1 stack, 1
// excluded unit) gives the golden `units --format json`, with a fake terragrunt replaying
// recorded find and render output.
func TestSpec_discovery_1(t *testing.T) {
	t.Parallel()

	bin := fakeTerragrunt(t, "find.json")
	res := runCLI(t, bin, "units", "tg-live", "--format", "json")
	require.Equal(t, 0, res.code, res.stderr)
	require.NoError(t, validateJSON("report-v1.json", []byte(res.stdout)))
	testutil.Golden(t, filepath.Join(goldenDir, "tg-live.json"), stableJSON(res.stdout))

	res = runCLI(t, bin, "units", "tg-live", "--format", "tty")
	require.Equal(t, 0, res.code, res.stderr)
	testutil.Golden(t, filepath.Join(goldenDir, "tg-live.txt"), []byte(res.stdout))

	res = runCLI(t, bin, "units", "tg-live", "--format", "md")
	require.Equal(t, 0, res.code, res.stderr)
	testutil.Golden(t, filepath.Join(goldenDir, "tg-live.md"), []byte(res.stdout))
}

// TestSpec_discovery_2: plain Terraform with 3 roots; the one with cloud {} is an unsupported
// backend, not an error. terragrunt isn't called.
func TestSpec_discovery_2(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{})
	res := runCLI(t, bin, "units", "plain-tf", "--format", "json")
	require.Equal(t, 0, res.code, res.stderr)
	require.NoError(t, validateJSON("report-v1.json", []byte(res.stdout)))
	testutil.Golden(t, filepath.Join(goldenDir, "plain-tf.json"), stableJSON(res.stdout))
	assert.Empty(t, fakebin.Calls(t, bin))
}

// TestSpec_discovery_4: --filter './live/prod/**' reaches terragrunt unchanged.
func TestSpec_discovery_4(t *testing.T) {
	t.Parallel()

	bin := fakeTerragrunt(t, "find.json")
	res := runCLI(t, bin, "units", "tg-live", "--filter", prodFilter, "--render", "never", "--format", "json")
	require.Equal(t, 0, res.code, res.stderr)

	calls := fakebin.Calls(t, bin)
	require.Len(t, calls, 1, "render never runs only find")
	assert.Equal(t, []string{
		"find", "--json", "--dag", "--dependencies", "--exclude", "--no-color", "--log-level", "error",
		"--filter", prodFilter,
	}, calls[0].Args)
	assert.NotContains(t, res.stdout, "live/nonprod")
	assert.Contains(t, res.stdout, `"path": "live/prod/eu-west-1/prod/vpc"`)
}

func TestUnitsRendersWithTFPath(t *testing.T) {
	t.Parallel()

	bin := fakeTerragrunt(t, "find.json")
	res := runCLI(t, bin, "units", "tg-live", "--tf-path", "tofu", "--format", "json")
	require.Equal(t, 0, res.code, res.stderr)
	var rendered []string
	for _, c := range fakebin.Calls(t, bin) {
		if c.Args[0] == "render" {
			rendered = append(rendered, c.Args[2])
			assert.Equal(t, []string{"--json", "--no-dependency-outputs", "--tf-path", "tofu"}, c.Args[3:7])
		}
	}
	assert.ElementsMatch(t, []string{"live/prod/eu-west-1/prod/dns", "live/prod/eu-west-1/prod/secrets"}, rendered)
}

func TestUnitsGenerateStacks(t *testing.T) {
	t.Parallel()

	bin := fakeTerragrunt(t, "find-generated.json")
	res := runCLI(t, bin, "units", "tg-live", "--generate-stacks", "--format", "plain")
	require.Equal(t, 0, res.code, res.stderr)
	calls := fakebin.Calls(t, bin)
	require.NotEmpty(t, calls)
	assert.Equal(t, []string{"stack", "generate", "--no-color", "--log-level", "error"}, calls[0].Args)
	assert.Regexp(t, `services/\.terragrunt-stack/api +terragrunt +s3://acme-tfstate-acme-prod/`, res.stdout)
	assert.NotContains(t, res.stdout, "not generated")
}

func TestUnitsErrors(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{})
	res := runCLI(t, bin, "units", "missing", "plain-tf", "--format", "json")
	assert.Equal(t, cli.ExitError, res.code)
	assert.Contains(t, res.stdout, `"target": "missing"`)
	assert.Contains(t, res.stdout, `"path": "network"`)
	assert.Contains(t, res.stderr, "discover units: missing: resolve path:")

	res = runCLI(t, bin, "units", "--format", "sarif")
	assert.Equal(t, cli.ExitUsage, res.code)

	res = runCLI(t, bin, "units", "--render", "sometimes")
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "discovery.render")
}

// TestUnitsAWSAccountID resolves a bucket named after get_aws_account_id by the fast path, with
// the account of the default credentials (AWS_PROFILE) looked up through STS.
func TestUnitsAWSAccountID(t *testing.T) {
	t.Parallel()

	repo := t.TempDir()
	files := map[string]string{
		"root.hcl": `remote_state {
  backend = "s3"
  config = {
    bucket       = "tfstate-${get_aws_account_id()}"
    key          = "${path_relative_to_include()}/terraform.tfstate"
    region       = "eu-west-1"
    use_lockfile = true
  }
}
`,
		"live/app/terragrunt.hcl": "include \"root\" {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n",
		"live/db/terragrunt.hcl":  "include \"root\" {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n",
	}
	for name, content := range files {
		p := filepath.Join(repo, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{{
		Args:   []string{"find"},
		Stdout: `[{"type":"unit","path":"live/app"},{"type":"unit","path":"live/db"}]`,
	}}})
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-live", Region: "eu-west-1"}}, "")
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env: app.Env{
			"PATH=" + filepath.Dir(bin), "HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"NO_COLOR=1", "AWS_PROFILE=acme-live",
		},
		Clock:         func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		Getwd:         func() (string, error) { return repo, nil },
		HTTPTransport: awstest.Replay(t, filepath.Join(cassetteDir, "aws", "units-account-id")),
	}
	code := cli.Execute(t.Context(), d, []string{"units", "--render", "never"})
	require.Equal(t, cli.ExitOK, code, stderr.String())
	assert.Contains(t, stdout.String(), "s3://tfstate-111111111111/live/app/terraform.tfstate (eu-west-1)")
	assert.Contains(t, stdout.String(), "s3://tfstate-111111111111/live/db/terraform.tfstate (eu-west-1)")
	assert.Contains(t, stdout.String(), "2 units: 2 fast-path, 0 render, 0 static, 0 unresolved")
}
