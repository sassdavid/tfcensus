package drift_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/config"
	"github.com/sassdavid/tfcensus/internal/drift"
	"github.com/sassdavid/tfcensus/internal/execx"
)

var now = time.Date(2026, 11, 12, 8, 0, 0, 0, time.UTC) //nolint:gochecknoglobals // test clock

func plan(t *testing.T, s string) *tfjson.Plan {
	t.Helper()
	var p tfjson.Plan
	require.NoError(t, json.Unmarshal([]byte(s), &p))
	return &p
}

func input(t *testing.T, unit, planJSON string) drift.Input {
	t.Helper()
	return drift.Input{
		Unit: drift.Unit{Path: unit, Status: drift.StatusClean, Outputs: []string{}},
		Plan: plan(t, planJSON),
	}
}

func paths(attrs []drift.Attr) []string {
	out := make([]string, len(attrs))
	for i, a := range attrs {
		out[i] = a.Path + " " + string(a.Kind)
	}
	return out
}

const fullPlan = `{
  "format_version": "1.2",
  "resource_drift": [
    {"address": "aws_s3_bucket.a", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
      "before": {"tags": {"Team": "a"}, "versioning": [{"enabled": true}]},
      "after": {"tags": {"Team": "b"}, "versioning": [{"enabled": true}]}}},
    {"address": "aws_s3_bucket.kept", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
      "before": {"tags": {"Team": "a"}, "acl": "private"}, "after": {"tags": {"Team": "b"}, "acl": "private"}}},
    {"address": "aws_instance.web", "mode": "managed", "type": "aws_instance", "change": {"actions": ["update"],
      "before": {"ami": "ami-1", "user_data": "x"}, "after": {"ami": "ami-2", "user_data": "x"}}},
    {"address": "data.aws_caller_identity.me", "mode": "data", "type": "aws_caller_identity",
      "change": {"actions": ["update"], "before": {}, "after": {"id": "1"}}}
  ],
  "resource_changes": [
    {"address": "aws_s3_bucket.a", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
      "before": {"tags": {"Team": "b"}}, "after": {"tags": {"Team": "a"}}}},
    {"address": "aws_s3_bucket.kept", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
      "before": {"acl": "private"}, "after": {"acl": "public-read"}}},
    {"address": "aws_instance.web", "mode": "managed", "type": "aws_instance", "change": {"actions": ["delete", "create"],
      "before": {"ami": "ami-2"}, "after": {"ami": "ami-1"}, "replace_paths": [["ami"]]}},
    {"address": "aws_sqs_queue.new", "mode": "managed", "type": "aws_sqs_queue", "change": {"actions": ["create"],
      "before": null, "after": {"name": "q"}}}
  ]
}`

func TestFullModeReverts(t *testing.T) {
	t.Parallel()

	rep := drift.Analyze([]drift.Input{input(t, "u", fullPlan)}, nil, drift.Options{
		Mode: drift.ModeFull, Collapse: drift.CollapseNone, Now: now,
	})
	require.Len(t, rep.Changes, 3, "data sources aren't drift")
	byAddr := map[string]drift.Change{}
	for _, c := range rep.Changes {
		byAddr[c.Address] = c
	}
	assert.Equal(t, drift.ActionUpdate, byAddr["aws_s3_bucket.a"].Revert, "the plan sets the drifted tag back")
	assert.Empty(t, byAddr["aws_s3_bucket.kept"].Revert, "the plan changes another attribute only")
	assert.Equal(t, drift.ActionReplace, byAddr["aws_instance.web"].Revert)
	assert.True(t, byAddr["aws_instance.web"].Attributes[0].ForcesReplacement)
	assert.Equal(t, []string{"tags.Team changed"}, paths(byAddr["aws_s3_bucket.a"].Attributes))
	u := rep.Units[0]
	assert.Equal(t, drift.StatusDrifted, u.Status)
	assert.Equal(t, 3, u.Drifted)
	assert.Equal(t, 2, u.Reverts)
	assert.Equal(t, 2, u.Pending, "the kept bucket's acl and the new queue")
	assert.Equal(t, 2, rep.Summary.Reverts)
}

func TestDiffMasksSensitiveAndUnknown(t *testing.T) {
	t.Parallel()

	const p = `{"format_version": "1.2", "resource_drift": [
	  {"address": "aws_db_instance.x", "mode": "managed", "type": "aws_db_instance", "change": {"actions": ["update"],
	    "before": {"password": "s1", "tags": {"a": "1", "kubernetes.io/role": "x"}, "rules": [{"port": 1}],
	               "settings": {"k": "v1"}, "id": "i"},
	    "after": {"password": "s2", "tags": {"b": "2", "kubernetes.io/role": "y"}, "rules": [{"port": 2}, {"port": 3}],
	              "settings": {"k": "v2"}},
	    "before_sensitive": {"password": true, "settings": true},
	    "after_sensitive": {"password": true, "settings": true},
	    "after_unknown": {"id": true}}}]}`
	rep := drift.Analyze([]drift.Input{input(t, "u", p)}, nil, drift.Options{
		Mode: drift.ModeRefreshOnly, Collapse: drift.CollapseShape, ShowAttributes: true, Now: now,
	})
	attrs := rep.Changes[0].Attributes
	assert.Equal(t, []string{
		"id changed", "password changed", "rules[0].port changed", "rules[1] added", "settings changed",
		"tags.a removed", "tags.b added", `tags["kubernetes.io/role"] changed`,
	}, paths(attrs))
	byPath := map[string]drift.Attr{}
	for _, a := range attrs {
		byPath[a.Path] = a
	}
	assert.Equal(t, drift.MaskUnknown, *byPath["id"].After)
	assert.True(t, byPath["id"].Unknown)
	for _, p := range []string{"password", "settings"} {
		assert.True(t, byPath[p].Sensitive)
		assert.Equal(t, drift.MaskSensitive, *byPath[p].Before)
		assert.Equal(t, drift.MaskSensitive, *byPath[p].After)
	}
	assert.Equal(t, `{"port":3}`, *byPath["rules[1]"].After)
	assert.Equal(t, []string{
		"id", "password", "rules[*]", "rules[*].port", "settings", "tags.a", "tags.b",
		`tags["kubernetes.io/role"]`,
	}, groupPaths(rep.Groups[0]))
}

func groupPaths(g drift.Group) []string {
	out := make([]string, len(g.Attributes))
	for i, a := range g.Attributes {
		out[i] = a.Path
	}
	return out
}

func tagPlan(value string) string {
	return `{"format_version": "1.2", "resource_drift": [
	  {"address": "aws_s3_bucket.b", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
	    "before": {"tags": {"Team": "x"}}, "after": {"tags": {"Team": "` + value + `"}}}}]}`
}

func TestCollapseModes(t *testing.T) {
	t.Parallel()

	inputs := func() []drift.Input {
		return []drift.Input{input(t, "a", tagPlan("y")), input(t, "b", tagPlan("y")), input(t, "c", tagPlan("z"))}
	}
	for _, tc := range []struct {
		mode  drift.Collapse
		sizes []int
	}{
		{drift.CollapseShape, []int{3}},
		{drift.CollapseStrict, []int{2, 1}},
		{drift.CollapseNone, []int{1, 1, 1}},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			t.Parallel()
			rep := drift.Analyze(inputs(), nil, drift.Options{
				Mode: drift.ModeRefreshOnly, Collapse: tc.mode, ShowAttributes: true, Now: now,
			})
			var sizes []int
			for _, g := range rep.Groups {
				sizes = append(sizes, len(g.Members))
			}
			assert.Equal(t, tc.sizes, sizes)
			assert.Equal(t, "g1", rep.Changes[0].Group)
		})
	}
	rep := drift.Analyze(inputs(), nil, drift.Options{
		Mode: drift.ModeRefreshOnly, Collapse: drift.CollapseShape, ShowAttributes: true, Now: now,
	})
	assert.Nil(t, rep.Groups[0].Attributes[0].After, "members disagree on the value")
}

func noiseRules(t *testing.T, neverHide []string, rules ...config.NoiseRule) *drift.NoiseRules {
	t.Helper()
	n, err := drift.NewNoiseRules(rules, neverHide)
	require.NoError(t, err)
	return n
}

func TestNoise(t *testing.T) {
	t.Parallel()

	const p = `{"format_version": "1.2", "resource_drift": [
	  {"address": "aws_s3_bucket.b", "mode": "managed", "type": "aws_s3_bucket", "change": {"actions": ["update"],
	    "before": {"tags": {"T": "1"}, "tags_all": {"T": "1"}, "etag": "a"},
	    "after": {"tags": {"T": "2"}, "tags_all": {"T": "2"}, "etag": "b"}}},
	  {"address": "aws_s3_object.o", "mode": "managed", "type": "aws_s3_object", "change": {"actions": ["update"],
	    "before": {"etag": "a"}, "after": {"etag": "b"}}},
	  {"address": "aws_sqs_queue.q", "mode": "managed", "type": "aws_sqs_queue", "change": {"actions": ["delete"],
	    "before": {"name": "q"}, "after": null}}]}`
	rules := noiseRules(t, []string{"delete"},
		config.NoiseRule{Preset: drift.PresetAWSTagsAll},
		config.NoiseRule{Preset: drift.PresetComputedHashes},
		config.NoiseRule{Name: "old", Type: "aws_s3_bucket", Attribute: "tags", Reason: "r", Expires: "2026-11-11"},
		config.NoiseRule{Name: "today", Type: "aws_s3_bucket", Attribute: "tags.*", Reason: "r", Expires: "2026-11-12"},
		config.NoiseRule{Name: "queues", Unit: "live/*", Type: "aws_sqs_*", Reason: "r"},
	)
	rep := drift.Analyze([]drift.Input{input(t, "live/x", p)}, nil, drift.Options{
		Mode: drift.ModeRefreshOnly, Collapse: drift.CollapseShape, Noise: rules, Now: now,
	})
	byAddr := map[string]drift.Change{}
	for _, c := range rep.Changes {
		byAddr[c.Address] = c
	}
	bucket := byAddr["aws_s3_bucket.b"]
	require.NotNil(t, bucket.Hidden, "every attribute is noise")
	assert.Equal(t, "preset/computed-hashes", bucket.Attributes[0].Noise.Rule)
	assert.Equal(t, "user/today", bucket.Attributes[1].Noise.Rule, "a rule applies through its last day")
	assert.Equal(t, "preset/aws-tags-all", bucket.Attributes[2].Noise.Rule)
	assert.Equal(t, "preset/computed-hashes", byAddr["aws_s3_object.o"].Hidden.Rule)
	queue := byAddr["aws_sqs_queue.q"]
	assert.Nil(t, queue.Hidden)
	assert.Equal(t, "delete is in never_hide, so user/queues doesn't apply", queue.Protected)
	assert.Equal(t, []string{
		`noise rule "old" is past its expires date (2026-11-11) and no longer hides drift: 1 change(s) reported again`,
	}, rep.Warnings)
	assert.Equal(t, 2, rep.Summary.Hidden)
	assert.Equal(t, 1, rep.Summary.Resources)
}

func TestNoiseErrors(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		rule config.NoiseRule
		want string
	}{
		{config.NoiseRule{Preset: "nope"}, `unknown preset "nope"`},
		{config.NoiseRule{Preset: drift.PresetAWSTagsAll, Unit: "x"}, "a preset entry takes no other key"},
		{config.NoiseRule{Name: "n"}, "set preset, or name and reason"},
		{config.NoiseRule{Name: "n", Reason: "r"}, "set unit, type, address, attribute or action"},
		{config.NoiseRule{Name: "n", Reason: "r", Action: "move"}, `invalid action "move"`},
		{config.NoiseRule{Name: "n", Reason: "r", Type: "x", Expires: "soon"}, `expires "soon": want YYYY-MM-DD`},
		{config.NoiseRule{Name: "n", Reason: "r", Attribute: ".tags"}, "starts with a dot"},
		{config.NoiseRule{Name: "n", Reason: "r", Attribute: `tags["x`}, "unterminated quoted key"},
		{config.NoiseRule{Name: "n", Reason: "r", Attribute: "a[1"}, "missing ]"},
	} {
		_, err := drift.NewNoiseRules([]config.NoiseRule{tc.rule}, nil)
		require.ErrorIs(t, err, drift.ErrNoise, tc.want)
		require.ErrorContains(t, err, tc.want)
	}
	_, err := drift.NewNoiseRules(nil, []string{"destroy"})
	require.ErrorContains(t, err, `drift.never_hide: invalid action "destroy"`)
	_, err = drift.NewNoiseRules([]config.NoiseRule{{Preset: "aws-tags-all"}, {Preset: "aws-tags-all"}}, nil)
	require.ErrorContains(t, err, "preset/aws-tags-all is listed twice")
}

func TestNoisePatterns(t *testing.T) {
	t.Parallel()

	const p = `{"format_version": "1.2", "resource_drift": [
	  {"address": "aws_security_group.s", "mode": "managed", "type": "aws_security_group", "change": {"actions": ["update"],
	    "before": {"ingress": [{"cidr": ["a"]}, {"cidr": ["b"]}], "tags": {"k8s.io/x": "1", "Other": "1"}},
	    "after": {"ingress": [{"cidr": ["a2"]}, {"cidr": ["b2"]}], "tags": {"k8s.io/x": "2", "Other": "2"}}}}]}`
	rules := noiseRules(t, nil,
		config.NoiseRule{Name: "first", Attribute: "ingress[0].cidr", Reason: "r"},
		config.NoiseRule{Name: "quoted", Attribute: `tags["k8s.io/x"]`, Reason: "r"},
	)
	rep := drift.Analyze([]drift.Input{input(t, "u", p)}, nil, drift.Options{
		Mode: drift.ModeRefreshOnly, Collapse: drift.CollapseShape, Noise: rules, Now: now,
	})
	assert.Equal(t, []string{"ingress[1].cidr[0] changed", "tags.Other changed"}, paths(rep.Changes[0].Visible()))
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

func TestLoad(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "plans", "a", "tfplan.json"), tagPlan("y"))
	writeFile(t, filepath.Join(dir, "plans", "bad", "tfplan.json"), `{"format_version": "9.0"}`)
	writeFile(t, filepath.Join(dir, "plans", "extra", "tfplan.json"), tagPlan("y"))
	writeFile(t, filepath.Join(dir, "report.json"), `[
	  {"Name": "a", "Result": "succeeded"}, {"Name": "bad", "Result": "succeeded"},
	  {"Name": "missing", "Result": "succeeded"}, {"Name": "skipped", "Result": "early exit", "Reason": "ancestor error"},
	  {"Name": "odd", "Result": "paused"}]`)
	inputs, warnings, err := drift.Load(drift.Source{
		PlanDir: filepath.Join(dir, "plans"), Report: filepath.Join(dir, "report.json"), Prefix: "repo",
	})
	require.NoError(t, err)
	got := map[string]string{}
	for _, in := range inputs {
		got[in.Unit.Path] = string(in.Unit.Status) + ": " + in.Unit.Reason
	}
	assert.Equal(t, map[string]string{
		"repo/a":       "clean: ",
		"repo/bad":     `failed: parse plan: unsupported plan format version: "9.0.0" does not satisfy ">= 0.1, < 2.0"`,
		"repo/extra":   "clean: ",
		"repo/missing": "failed: succeeded but wrote no tfplan.json",
		"repo/skipped": "early-exit: ancestor error",
		"repo/odd":     `failed: unknown run result "paused"`,
	}, got)
	assert.Equal(t, []string{"unit extra has a plan but isn't in the run report"}, warnings)

	_, warnings, err = drift.Load(drift.Source{PlanDir: filepath.Join(dir, "plans")})
	require.NoError(t, err)
	assert.Equal(t, []string{"no run report: units that failed before writing a plan aren't listed"}, warnings)

	for _, src := range []drift.Source{
		{PlanDir: filepath.Join(dir, "nope")},
		{PlanDir: filepath.Join(dir, "report.json")},
		{PlanDir: filepath.Join(dir, "plans"), Report: filepath.Join(dir, "plans", "a", "tfplan.json")},
	} {
		_, _, err = drift.Load(src)
		require.ErrorIs(t, err, drift.ErrSource)
	}
}

// fakeRunner writes a report when told to and records the command.
type fakeRunner struct {
	writeReport bool
	got         execx.Cmd
}

func (f *fakeRunner) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	f.got = c
	_, _ = c.Stdout.Write([]byte(`{"level":"error","working-dir":"./u","msg":"no"}` + "\n" + `not json`))
	if f.writeReport {
		for i, a := range c.Args {
			if a == "--report-file" {
				if err := os.WriteFile(c.Args[i+1], []byte("[]"), 0o600); err != nil {
					return nil, err
				}
			}
		}
	}
	return &execx.Result{ExitCode: 1}, &execx.ExitError{Name: c.Name, Code: 1}
}

func TestRunPlans(t *testing.T) {
	t.Parallel()

	opts := drift.RunOptions{Terragrunt: "tg", Terraform: "tofu", Mode: drift.ModeRefreshOnly}
	r := &fakeRunner{writeReport: true}
	out := t.TempDir()
	src, err := drift.RunPlans(t.Context(), r, "/repo", out, opts)
	require.NoError(t, err, "a non-zero exit with a report is a run with failed units")
	assert.Equal(t, filepath.Join(out, "report.json"), src.Report)
	assert.Equal(t, []string{"TG_TF_PATH=tofu"}, r.got.Env)
	assert.Equal(t, "/repo", r.got.Dir)
	assert.Negative(t, int64(r.got.Timeout), "plans can take as long as they need")

	_, err = drift.RunPlans(t.Context(), &fakeRunner{}, "/repo", t.TempDir(), opts)
	require.EqualError(t, err, "terragrunt run wrote no report: exit 1")
}
