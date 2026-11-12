package cli_test

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
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
	"github.com/sassdavid/tfcensus/internal/testutil/sarifcheck"
)

const (
	moveRepo    = "../../testdata/repos/tf-move"
	moveStates  = "../../testdata/move/states"
	moveGolden  = "../../testdata/golden/move/cli"
	moveBucket  = "fixture-tfstate-111111111111"
	moveFile    = "migrations/split_network.hcl"
	moveAccount = "111111111111"
)

// moveFake is an S3 and STS fake holding the fixture states, with bucket versioning on unless
// unversioned is set.
func moveFake(t *testing.T, unversioned bool) *awstest.Fake {
	t.Helper()
	fake := awstest.NewFake(t, time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC))
	fake.Keys = map[string]string{"AKIAFAKEDEFAULT": moveAccount}
	fake.Regions = map[string]string{moveBucket: "eu-west-1"}
	fake.CreateBucket(moveBucket)
	if !unversioned {
		fake.Stubs = map[string]awstest.Stub{"s3:" + moveBucket + "?versioning": versioning("Enabled", "")}
	}
	for unit, file := range map[string]string{
		"live/network": "network.tfstate", "live/network-endpoints": "network-endpoints.tfstate",
	} {
		b, err := os.ReadFile(filepath.Join(moveStates, file))
		require.NoError(t, err)
		fake.Put(moveBucket, unit+"/terraform.tfstate", b)
	}
	return fake
}

// moveTerragrunt is a fake terragrunt whose plans have no changes.
func moveTerragrunt(t *testing.T) string {
	t.Helper()
	return moveTerragruntPlan(t, "noop.json")
}

// moveTerragruntPlan is a fake terragrunt whose every plan is the named fixture.
func moveTerragruntPlan(t *testing.T, name string) string {
	t.Helper()
	plan, err := filepath.Abs(filepath.Join("../../testdata/move/plans", name))
	require.NoError(t, err)
	return fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{
		{Args: []string{"run", "--tf-path", "terraform", "--", "show"}, StdoutFile: plan},
		{Args: []string{"run"}},
	}})
}

func runMove(t *testing.T, rt http.RoundTripper, tg string, args ...string) cliResult {
	t.Helper()
	// Plans write an override file into the unit directories, so each run gets its own copy.
	cwd := filepath.Join(t.TempDir(), "tf-move")
	require.NoError(t, os.CopyFS(cwd, os.DirFS(moveRepo)))
	home := fleetHome(t)
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env: app.Env{
			"HOME=" + home, "XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"), "NO_COLOR=1", "AWS_EC2_METADATA_DISABLED=true",
			"PATH=" + os.Getenv("PATH"),
		},
		Clock:         func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
		Getwd:         func() (string, error) { return cwd, nil },
		HTTPTransport: rt,
	}
	code := cli.Execute(t.Context(), d, append(append([]string{"move"}, args...), "--tg-path", tg))
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

// TestMoveGolden checks move plan in each format and move status over the tf-move fixture repo.
func TestMoveGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		file string
	}{
		{"plan tty", []string{"plan", moveFile, "--format", "tty"}, "plan.txt"},
		{"plan json", []string{"plan", moveFile, "--format", "json"}, "plan.json"},
		{"plan sarif", []string{"plan", moveFile, "--format", "sarif"}, "plan.sarif"},
		{"status", []string{"status", moveFile, "--format", "plain"}, "status.txt"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runMove(t, moveFake(t, false), moveTerragrunt(t), tt.args...)
			require.Equal(t, cli.ExitOK, res.code, res.stderr)
			out := []byte(res.stdout)
			switch filepath.Ext(tt.file) {
			case ".json":
				require.NoError(t, validateJSON("move-v1.json", out))
				require.NoError(t, validateJSON("report-v1.json", out))
				out = stableJSON(res.stdout)
			case ".sarif":
				require.NoError(t, sarifcheck.Validate(out))
				out = stableSARIF(t, res.stdout)
			}
			testutil.Golden(t, filepath.Join(moveGolden, tt.file), out)
			assertNoMoveSecrets(t, tt.name, res.stdout+res.stderr)
		})
	}
}

// TestMoveRejected: a plan that would create a moved resource rejects the block with exit 3, in
// text and SARIF, and apply pushes nothing.
func TestMoveRejected(t *testing.T) {
	t.Parallel()
	for _, format := range []string{"plain", "sarif"} {
		t.Run(format, func(t *testing.T) {
			t.Parallel()
			tg := moveTerragruntPlan(t, "wrong-account.json")
			res := runMove(t, moveFake(t, false), tg, "plan", moveFile, "--block", "split_network", "--format", format)
			require.Equal(t, cli.ExitFindings, res.code, res.stderr)
			out := []byte(res.stdout)
			if format == "sarif" {
				require.NoError(t, sarifcheck.Validate(out))
				out = stableSARIF(t, res.stdout)
			}
			testutil.Golden(
				t,
				filepath.Join(moveGolden, "rejected."+map[string]string{"plain": "txt", "sarif": "sarif"}[format]),
				out,
			)
			assertNoMoveSecrets(t, format, res.stdout+res.stderr)
		})
	}
	tg := moveTerragruntPlan(t, "wrong-account.json")
	res := runMove(t, moveFake(t, false), tg, "apply", moveFile, "--yes")
	require.Equal(t, cli.ExitFindings, res.code, res.stderr)
	for _, c := range fakebin.Calls(t, tg) {
		assert.NotContains(t, c.Args, "push")
	}
}

// TestMoveApplyNeedsYes: without a terminal and without --yes, apply exits 2 and writes nothing.
func TestMoveApplyNeedsYes(t *testing.T) {
	t.Parallel()
	fake := moveFake(t, false)
	tg := moveTerragrunt(t)
	res := runMove(t, fake, tg, "apply", moveFile)
	require.Equal(t, cli.ExitUsage, res.code, res.stderr)
	assert.Contains(t, res.stderr, "refusing to modify state without --yes")
	for _, c := range fakebin.Calls(t, tg) {
		assert.NotContains(t, c.Args, "push", "nothing is pushed")
	}
	res = runMove(t, fake, tg, "status", moveFile, "--format", "json")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.NotContains(t, res.stdout, `"applied"`)
}

// TestMoveRejectsUnversionedBucket: apply refuses a bucket without versioning (exit 3).
func TestMoveRejectsUnversionedBucket(t *testing.T) {
	t.Parallel()
	res := runMove(t, moveFake(t, true), moveTerragrunt(t), "apply", moveFile, "--yes", "--block", "split_network")
	require.Equal(t, cli.ExitFindings, res.code, res.stderr)
	assert.Contains(t, res.stdout, "MV-VERSIONING")
}

func TestMoveUsage(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"missing file", []string{"plan", "migrations/none.hcl"}, "read migration file"},
		{"unknown block", []string{"plan", moveFile, "--block", "nope"}, `no block "nope"`},
		{"bad var", []string{"plan", moveFile, "--var", "env"}, "want NAME=VALUE"},
		{"format", []string{"status", moveFile, "--format", "sarif"}, "sarif"},
		{"no file", []string{"plan"}, "accepts 1 arg"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runMove(t, moveFake(t, false), moveTerragrunt(t), tt.args...)
			require.Equal(t, cli.ExitUsage, res.code, res.stderr)
			assert.Contains(t, res.stderr, tt.want)
		})
	}
}

func assertNoMoveSecrets(t *testing.T, label, out string) {
	t.Helper()
	for _, s := range []string{"FIXTURE-SECRET", "10.20.0.0/16", "10.20.1.10", "com.amazonaws.eu-west-1.s3"} {
		assert.NotContains(t, out, s, "%s contains an attribute value", label)
	}
}
