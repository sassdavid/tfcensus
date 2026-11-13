package cli_test

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
)

const (
	driftRunDir    = driftFixtures + "/run"
	driftIngestDir = driftFixtures + "/ingest"
)

func absPath(t *testing.T, p string) string {
	t.Helper()
	a, err := filepath.Abs(p)
	require.NoError(t, err)
	return a
}

// driftConfig writes a config with the two presets and returns its path.
func driftConfig(t *testing.T) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tfcensus.yaml")
	cfg := "schema_version: 1\ndrift:\n  noise:\n    - preset: aws-tags-all\n    - preset: computed-hashes\n"
	require.NoError(t, os.WriteFile(p, []byte(cfg), 0o600))
	return p
}

// driftRun is the terragrunt rule that writes the run fixture's plans and report, logs NDJSON
// progress and exits 2, as -detailed-exitcode does when a plan has changes.
func driftRun(t *testing.T) fakebin.Rule {
	t.Helper()
	return fakebin.Rule{
		Args:       []string{"run", "--all"},
		StdoutFile: absPath(t, filepath.Join(driftRunDir, "log.ndjson")),
		ExitCode:   2,
		Files: []fakebin.File{
			{Flag: "--json-out-dir", Src: absPath(t, filepath.Join(driftRunDir, "plans"))},
			{Flag: "--report-file", Src: absPath(t, filepath.Join(driftRunDir, "report.json"))},
		},
	}
}

func runCalls(t *testing.T, bin string) []fakebin.Call {
	t.Helper()
	var out []fakebin.Call
	for _, c := range fakebin.Calls(t, bin) {
		if len(c.Args) > 0 && c.Args[0] == "run" {
			out = append(out, c)
		}
	}
	return out
}

type driftJSON struct {
	Data struct {
		Summary map[string]int `json:"summary"`
		Units   []struct {
			Path   string `json:"path"`
			Status string `json:"status"`
		} `json:"units"`
		Groups []struct {
			ID      string           `json:"id"`
			Members []map[string]any `json:"members"`
		} `json:"groups"`
	} `json:"data"`
	Errors []map[string]string `json:"errors"`
}

// TestSpec_drift_3: run mode with a fake terragrunt that writes fixture plans. The argv, the
// working directory and TG_TF_PATH are asserted, and the plan directory is removed afterwards.
func TestSpec_drift_3(t *testing.T) {
	t.Parallel()

	rules := append([]fakebin.Rule{driftRun(t)}, tgLiveRules(t, "find.json")...)
	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: rules, RecordEnv: []string{"TG_TF_PATH"}})
	res := runCLI(t, bin, "drift", "tg-live", "--filter", prodFilter, "--render", "never", "--format", "json",
		"--fail-on", "drift", "-v")
	require.Equal(t, cli.ExitFindings, res.code, res.stderr)
	require.NoError(t, validateJSON("drift-v1.json", []byte(res.stdout)))
	require.NoError(t, validateJSON("report-v1.json", []byte(res.stdout)))

	calls := runCalls(t, bin)
	require.Len(t, calls, 1)
	args := calls[0].Args
	i := slices.Index(args, "--json-out-dir")
	require.Positive(t, i)
	out := filepath.Dir(args[i+1])
	assert.Equal(t, []string{
		"run", "--all", "--filter", prodFilter, "--tf-path", "terraform",
		"--json-out-dir", filepath.Join(out, "plans"), "--report-file", filepath.Join(out, "report.json"),
		"--report-format", "json", "--log-format", "json", "--no-color",
		"--", "plan", "-refresh-only", "-lock=false", "-detailed-exitcode",
	}, args)
	assert.NoDirExists(t, out, "the plan directory is removed")
	repo, err := filepath.EvalSymlinks(absPath(t, filepath.Join(reposDir, "tg-live")))
	require.NoError(t, err)
	assert.Equal(t, repo, calls[0].Dir)
	assert.Equal(t, map[string]string{"TG_TF_PATH": "terraform"}, calls[0].Env)

	var rep driftJSON
	require.NoError(t, json.Unmarshal([]byte(res.stdout), &rep))
	status := map[string]string{}
	for _, u := range rep.Data.Units {
		status[u.Path] = u.Status
	}
	assert.Equal(t, map[string]string{
		"tg-live/live/prod/eu-central-1/prod/edge": "clean",
		"tg-live/live/prod/eu-west-1/prod/app":     "clean",
		"tg-live/live/prod/eu-west-1/prod/db":      "clean",
		"tg-live/live/prod/eu-west-1/prod/dns":     "clean",
		"tg-live/live/prod/eu-west-1/prod/legacy":  "excluded",
		"tg-live/live/prod/eu-west-1/prod/secrets": "failed",
		"tg-live/live/prod/eu-west-1/prod/vpc":     "drifted",
	}, status)
	require.Len(t, rep.Data.Groups, 1)
	assert.Equal(t, []map[string]string{
		{"target": "tg-live/live/prod/eu-west-1/prod/secrets", "message": "plan failed: run error"},
	}, rep.Errors)
	for _, line := range []string{
		"unit started unit=live/prod/eu-west-1/prod/vpc", "unit finished unit=live/prod/eu-west-1/prod/vpc",
	} {
		assert.Contains(t, res.stderr, line)
	}

	res = runCLI(t, bin, "drift", "tg-live", "--filter", prodFilter, "--render", "never", "--mode", "full")
	require.Equal(t, cli.ExitError, res.code, "the failed unit gives exit 1 without a gate: %s", res.stderr)
	calls = runCalls(t, bin)
	require.Len(t, calls, 2)
	assert.Equal(t, []string{"--", "plan", "-lock=false", "-detailed-exitcode"}, calls[1].Args[len(calls[1].Args)-4:])
}

// TestDriftRunWithoutReport: a terragrunt run that leaves no report is a repo error with its
// last stderr line, and the exit code is 1, or 3 with --fail-on errors.
func TestDriftRunWithoutReport(t *testing.T) {
	t.Parallel()

	rules := append([]fakebin.Rule{{
		Args: []string{"run", "--all"}, ExitCode: 1,
		Stderr: `{"level":"error","msg":"Unsupported terragrunt version"}` + "\n",
	}}, tgLiveRules(t, "find.json")...)
	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{Rules: rules})
	res := runCLI(t, bin, "drift", "tg-live", "--render", "never", "--format", "json")
	require.Equal(t, cli.ExitError, res.code, res.stderr)
	assert.Contains(t, res.stdout, "terragrunt run wrote no report: exit 1: Unsupported terragrunt version")
	assert.Contains(t, res.stderr, "1 repo(s) failed")

	res = runCLI(t, bin, "drift", "tg-live", "--render", "never", "--fail-on", "errors")
	assert.Equal(t, cli.ExitFindings, res.code, res.stderr)
}

// writeDriftUnits writes n units, each with its own drift shape, and their run report.
func writeDriftUnits(t *testing.T, dir string, n int) {
	t.Helper()
	report := make([]map[string]string, 0, n)
	for i := range n {
		unit := fmt.Sprintf("live/account-%03d/eu-west-1/service-%03d", i/10, i)
		before := map[string]any{}
		after := map[string]any{}
		for j := range 4 {
			attr := fmt.Sprintf("generated_setting_%03d_%d", i, j)
			before[attr], after[attr] = "old", "new"
		}
		p := map[string]any{
			"format_version": "1.2", "terraform_version": "1.17.2",
			"resource_drift": []map[string]any{{
				"address": "aws_ssm_parameter.setting", "mode": "managed", "type": "aws_ssm_parameter",
				"name": "setting", "provider_name": "registry.terraform.io/hashicorp/aws",
				"change": map[string]any{"actions": []string{"update"}, "before": before, "after": after},
			}},
		}
		b, err := json.Marshal(p)
		require.NoError(t, err)
		require.NoError(t, os.MkdirAll(filepath.Join(dir, "plans", unit), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, "plans", unit, "tfplan.json"), b, 0o600))
		report = append(report, map[string]string{"Name": unit, "Result": "succeeded"})
	}
	b, err := json.Marshal(report)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "report.json"), b, 0o600))
}

// TestSpec_drift_4: --format md stays under 60,000 characters (--md-max-chars) for a generated
// 300-unit run, cuts between groups and ends with a truncation footer.
func TestSpec_drift_4(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeDriftUnits(t, dir, 300)
	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{})
	res := runCLI(t, bin, "drift", "--from-json-out-dir", filepath.Join(dir, "plans"),
		"--report-file", filepath.Join(dir, "report.json"), "--format", "md")
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.LessOrEqual(t, utf8.RuneCountInString(res.stdout), 60_000)
	assert.True(t, strings.HasPrefix(res.stdout, "300 units (300 drifted); 300 drifted resources in 300 groups"))
	assert.Regexp(t, "</details>\n\n---\n\nTruncated to 60000 characters: cut \\d+ sections, "+
		"starting at \"g\\d+: aws_ssm_parameter update in 1 unit\\(s\\)\"\\. "+
		"Run `tfcensus drift --format json` for the full report\\.\n$", res.stdout)
	assert.Empty(t, fakebin.Calls(t, bin), "ingest mode runs nothing")
}

// TestDriftAttributeValues greps every drift output and the debug log for attribute values of the
// fixture plans. Without --show-attributes only paths appear; sensitive values never appear.
func TestDriftAttributeValues(t *testing.T) {
	t.Parallel()

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{})
	cfg := driftConfig(t)
	plans, report := absPath(
		t,
		filepath.Join(driftIngestDir, "plans"),
	), absPath(
		t,
		filepath.Join(driftIngestDir, "report.json"),
	)
	values := []string{"10.42.0.0/16", "db.t4g", "finance", "c2hhMjU2", "team-a", "acme-assets"}
	sensitive := []string{"FIXTURE-SECRET"}
	for _, format := range []string{"json", "tty", "md", "github", "sarif"} {
		for _, show := range []bool{false, true} {
			args := []string{
				"drift", "--config", cfg, "--from-json-out-dir", plans, "--report-file", report,
				"--format", format, "-vv",
			}
			if show {
				args = append(args, "--show-attributes")
			}
			res := runCLI(t, bin, args...)
			require.Equal(t, cli.ExitError, res.code, res.stderr)
			label := fmt.Sprintf("%s show=%v", format, show)
			for _, s := range sensitive {
				assert.NotContains(t, res.stdout+res.stderr, s, "%s contains a sensitive value", label)
			}
			if show {
				continue
			}
			for _, s := range values {
				assert.NotContains(t, res.stdout+res.stderr, s, "%s contains an attribute value", label)
			}
		}
	}
	res := runCLI(t, bin, "drift", "--config", cfg, "--from-json-out-dir", plans, "--report-file", report,
		"--show-attributes", "--format", "json")
	assert.Contains(t, res.stdout, `"after": "\"10.42.0.0/16\""`, "--show-attributes shows values")
	assert.Contains(t, res.stdout, `"before": "(sensitive)"`)
}
