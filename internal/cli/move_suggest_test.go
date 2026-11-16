package cli_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

const (
	suggestPlans  = "../../testdata/move/suggest/plans"
	suggestGolden = "../../testdata/golden/move/suggest"
)

func suggestUnitArgs() []string {
	return []string{
		"suggest", "--from", "live/network", "--to", "live/network", "--to", "live/network-sg",
		"--to", "live/network-endpoints",
	}
}

func suggestPlanDir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.Abs(suggestPlans)
	require.NoError(t, err)
	return dir
}

// suggestTerragrunt is a fake terragrunt whose plan in each unit is that unit's fixture.
func suggestTerragrunt(t *testing.T) string {
	t.Helper()
	show := []string{"run", "--tf-path", "terraform", "--", "show"}
	var rules []fakebin.Rule
	for _, unit := range []string{"live/network", "live/network-sg", "live/network-endpoints"} {
		rules = append(rules, fakebin.Rule{
			Args: show, Dir: unit, StdoutFile: filepath.Join(suggestPlanDir(t), unit, "tfplan.json"),
		})
	}
	rules = append(rules, fakebin.Rule{Args: []string{"run"}})
	return fakebin.Install(
		t,
		"terragrunt",
		fakebin.Spec{Rules: rules, RecordEnv: []string{"TG_TF_PATH", "TF_WORKSPACE"}},
	)
}

func assertNoSuggestSecrets(t *testing.T, label, out string) {
	t.Helper()
	assertNoMoveSecrets(t, label, out)
	for _, s := range []string{"vpc-0f1e2d3c4b5a69788", "sg-0a1b2c3d4e5f60718", "/app/db/password", "rtb-0aa", "Statement"} {
		assert.NotContains(t, out, s, "%s contains an attribute value", label)
	}
}

// TestMoveSuggestGolden runs move suggest on the fixture plans in each format.
func TestMoveSuggestGolden(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		args   []string
		stdout string
		stderr string
	}{
		{"tty", []string{"--format", "tty"}, "draft.txt", "summary.txt"},
		{
			"json rule",
			[]string{"--format", "plain", "--ignore", "json:aws_vpc_endpoint:policy"},
			"draft-json-rule.txt",
			"summary-json-rule.txt",
		},
		{"json", []string{"--format", "json", "--ignore", "json:aws_vpc_endpoint:policy"}, "suggest.json", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			args := append(append(suggestUnitArgs(), "--plan-dir", suggestPlanDir(t)), tt.args...)
			res := runMove(t, moveFake(t, false), suggestTerragrunt(t), args...)
			require.Equal(t, cli.ExitOK, res.code, res.stderr)
			out := []byte(res.stdout)
			if filepath.Ext(tt.stdout) == ".json" {
				require.NoError(t, validateJSON("move-suggest-v1.json", out))
				require.NoError(t, validateJSON("report-v1.json", out))
				out = stableJSON(res.stdout)
			}
			testutil.Golden(t, filepath.Join(suggestGolden, tt.stdout), out)
			if tt.stderr != "" {
				testutil.Golden(t, filepath.Join(suggestGolden, tt.stderr), []byte(res.stderr))
			}
			assertNoSuggestSecrets(t, tt.name, res.stdout+res.stderr)
		})
	}
}

// TestMoveSuggestTerragrunt plans every unit through terragrunt, one call chain per unit, without
// the lock and without writing state.
func TestMoveSuggestTerragrunt(t *testing.T) {
	t.Parallel()
	tg := suggestTerragrunt(t)
	res := runMove(t, moveFake(t, false), tg, append(suggestUnitArgs(), "--format", "plain")...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	golden, err := os.ReadFile(filepath.Join(suggestGolden, "draft.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(golden), res.stdout)
	assertNoSuggestSecrets(t, "terragrunt", res.stdout+res.stderr)

	planned := map[string]int{}
	for _, c := range fakebin.Calls(t, tg) {
		require.GreaterOrEqual(t, len(c.Args), 4, c.Args)
		assert.Equal(t, []string{"run", "--tf-path", "terraform", "--"}, c.Args[:4])
		assert.Equal(t, "terraform", c.Env["TG_TF_PATH"])
		assert.Empty(t, c.Env["TF_WORKSPACE"])
		assert.NotEqual(t, "push", c.Args[len(c.Args)-1])
		assert.NotContains(t, c.Args, "state", "suggest never touches state")
		if c.Args[4] == "plan" {
			assert.Contains(t, c.Args, "-lock=false")
			planned[filepath.Base(c.Dir)]++
		}
	}
	assert.Equal(t, map[string]int{"network": 1, "network-sg": 1, "network-endpoints": 1}, planned)
}

// TestMoveSuggestWorkspace plans a non-default workspace with TF_WORKSPACE.
func TestMoveSuggestWorkspace(t *testing.T) {
	t.Parallel()
	tg := suggestTerragrunt(t)
	res := runMove(t, moveFake(t, false), tg, "suggest", "--from", "live/network:blue", "--to", "live/network-sg")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stdout, `from = "live/network:blue"`)
	ws := map[string]string{}
	for _, c := range fakebin.Calls(t, tg) {
		ws[filepath.Base(c.Dir)] = c.Env["TF_WORKSPACE"]
	}
	assert.Equal(t, map[string]string{"network": "blue", "network-sg": ""}, ws)
}

// TestMoveSuggestDraftFile writes the draft to --draft and refuses to overwrite it without --force.
func TestMoveSuggestDraftFile(t *testing.T) {
	t.Parallel()
	draft := filepath.Join(t.TempDir(), "split.hcl")
	args := append(suggestUnitArgs(), "--plan-dir", suggestPlanDir(t), "--draft", draft)

	res := runMove(t, moveFake(t, false), suggestTerragrunt(t), args...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Empty(t, res.stdout)
	assert.Contains(t, res.stderr, "wrote "+draft+"; check it with tfcensus move plan "+draft)
	written, err := os.ReadFile(draft)
	require.NoError(t, err)
	golden, err := os.ReadFile(filepath.Join(suggestGolden, "draft.txt"))
	require.NoError(t, err)
	assert.Equal(t, string(golden), string(written))

	res = runMove(t, moveFake(t, false), suggestTerragrunt(t), args...)
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "exists; pass --force to overwrite it")

	res = runMove(t, moveFake(t, false), suggestTerragrunt(t), append(args, "--force")...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
}

// TestMoveSuggestNoMoves writes no draft when the plans hold no move.
func TestMoveSuggestNoMoves(t *testing.T) {
	t.Parallel()
	draft := filepath.Join(t.TempDir(), "none.hcl")
	res := runMove(t, moveFake(t, false), suggestTerragrunt(t), "suggest", "--from", "live/network-sg",
		"--to", "live/network-endpoints", "--plan-dir", suggestPlanDir(t), "--draft", draft)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Contains(t, res.stderr, "no moves found; no draft written")
	assert.NoFileExists(t, draft)
}

func TestMoveSuggestUsage(t *testing.T) {
	t.Parallel()
	dir := suggestPlanDir(t)
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"no to", []string{"suggest", "--from", "live/network"}, `required flag(s) "to" not set`},
		{"bad rule", []string{"suggest", "--from", "a", "--to", "b", "--ignore", "regex:t:a"}, "invalid ignore rule"},
		{"absolute unit", []string{"suggest", "--from", "/live/network", "--to", "b"}, "must be relative"},
		{
			"workspace with plan dir",
			[]string{"suggest", "--from", "live/network:blue", "--to", "live/network-sg", "--plan-dir", dir},
			"can't name a workspace",
		},
		{
			"missing plan",
			[]string{"suggest", "--from", "live/missing", "--to", "live/network-sg", "--plan-dir", dir},
			"no plan for live/missing",
		},
		{"unknown unit", []string{"suggest", "--from", "live/missing", "--to", "live/network-sg"}, "not a unit"},
		{"sarif", []string{"suggest", "--from", "a", "--to", "b", "--format", "sarif"}, "sarif"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			res := runMove(t, moveFake(t, false), suggestTerragrunt(t), tt.args...)
			assert.Equal(t, cli.ExitUsage, res.code, res.stderr)
			assert.Contains(t, res.stderr, tt.want)
		})
	}
}

// TestMoveSuggestPlanFails reports a failed plan without terraform's output.
func TestMoveSuggestPlanFails(t *testing.T) {
	t.Parallel()
	tg := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: []fakebin.Rule{
		{
			Args:     []string{"run", "--tf-path", "terraform", "--", "plan"},
			Stderr:   "FIXTURE-SECRET in an error",
			ExitCode: 1,
		},
		{Args: []string{"run"}},
	}})
	res := runMove(t, moveFake(t, false), tg, "suggest", "--from", "live/network", "--to", "live/network-sg")
	assert.Equal(t, cli.ExitError, res.code)
	assert.Contains(t, res.stderr, "plan live/network: terragrunt plan exited with code 1")
	assert.NotContains(t, res.stderr, "FIXTURE-SECRET")
	assert.False(t, slices.ContainsFunc(strings.Split(res.stdout, "\n"), func(l string) bool { return l != "" }))
}
