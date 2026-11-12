package migrate_test

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/migrate"
)

// fakeRunner answers terragrunt calls by the terraform subcommand after "--".
type fakeRunner struct {
	t     *testing.T
	calls []execx.Cmd
	on    map[string]func(c execx.Cmd) (*execx.Result, error)
}

func (f *fakeRunner) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	f.calls = append(f.calls, c)
	i := slices.Index(c.Args, "--")
	require.GreaterOrEqual(f.t, i, 0, "terragrunt run -- <command>")
	if h, ok := f.on[c.Args[i+1]]; ok {
		return h(c)
	}
	return &execx.Result{}, nil
}

func envOf(c execx.Cmd, key string) string {
	for _, kv := range c.Env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			return v
		}
	}
	return ""
}

func TestTerragruntPlan(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	lock := filepath.Join(dir, ".terraform.lock.hcl")
	require.NoError(t, os.WriteFile(lock, []byte("# original\n"), 0o600))
	state := filepath.Join(t.TempDir(), "state.json")
	require.NoError(t, os.WriteFile(state, []byte("{}"), 0o600))
	planJSON, err := os.ReadFile(filepath.Join(fixtures, "plans", "wrong-account.json"))
	require.NoError(t, err)

	var override string
	r := &fakeRunner{t: t, on: map[string]func(execx.Cmd) (*execx.Result, error){
		"init": func(execx.Cmd) (*execx.Result, error) {
			b, readErr := os.ReadFile(filepath.Join(dir, migrate.OverrideFile))
			require.NoError(t, readErr, "the override is in place for init")
			override = string(b)
			require.NoError(t, os.WriteFile(lock, []byte("# changed by init\n"), 0o600))
			return &execx.Result{}, nil
		},
		"show": func(execx.Cmd) (*execx.Result, error) { return &execx.Result{Stdout: planJSON}, nil },
	}}
	tg := &migrate.Terragrunt{Runner: r, Bin: "terragrunt", Terraform: "terraform"}
	changes, err := tg.Plan(t.Context(), dir, state)
	require.NoError(t, err)
	assert.Equal(t, []migrate.ResourceChange{
		{Address: "aws_security_group.app", Mode: "managed", Actions: []string{"create"}},
		{Address: `aws_vpc_security_group_ingress_rule.app["https"]`, Mode: "managed", Actions: []string{"no-op"}},
	}, changes)

	abs, err := filepath.Abs(state)
	require.NoError(t, err)
	assert.Contains(t, override, `backend "local"`)
	assert.Contains(t, override, `path = "`+filepath.ToSlash(abs)+`"`)
	assert.NoFileExists(t, filepath.Join(dir, migrate.OverrideFile), "the override is removed")
	b, err := os.ReadFile(lock)
	require.NoError(t, err)
	assert.Equal(t, "# original\n", string(b), "the lock file is restored")

	var subs []string
	for _, c := range r.calls {
		assert.Equal(t, "terragrunt", c.Name)
		assert.Equal(t, dir, c.Dir)
		assert.Equal(t, []string{"run", "--tf-path", "terraform", "--"}, c.Args[:4])
		assert.Equal(t, "terraform", envOf(c, "TG_TF_PATH"))
		assert.NotEmpty(t, envOf(c, "TF_DATA_DIR"))
		assert.NotEmpty(t, envOf(c, "TG_DOWNLOAD_DIR"))
		subs = append(subs, c.Args[4])
	}
	assert.Equal(t, []string{"init", "plan", "show"}, subs)
	assert.Contains(t, r.calls[1].Args, "-lock=false")
}

func TestTerragruntPlanErrors(t *testing.T) {
	t.Parallel()
	fail := func(stderr string) func(execx.Cmd) (*execx.Result, error) {
		return func(execx.Cmd) (*execx.Result, error) {
			return &execx.Result{ExitCode: 1}, &execx.ExitError{Name: "terragrunt", Code: 1, Stderr: stderr}
		}
	}
	t.Run("plan output stays out of the error", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{t: t, on: map[string]func(execx.Cmd) (*execx.Result, error){
			"plan": fail(`Error: value "FIXTURE-SECRET" is invalid`),
		}}
		tg := &migrate.Terragrunt{Runner: r, Bin: "terragrunt", Terraform: "terraform"}
		_, err := tg.Plan(t.Context(), t.TempDir(), "")
		require.Error(t, err)
		assert.NotContains(t, err.Error(), "FIXTURE-SECRET")
		assert.Contains(t, err.Error(), "terragrunt plan exited with code 1")
	})
	t.Run("remote_state without generate", func(t *testing.T) {
		t.Parallel()
		r := &fakeRunner{t: t, on: map[string]func(execx.Cmd) (*execx.Result, error){
			"init": fail(
				`The backend configuration argument "bucket" given on the command line (-backend-config) is not expected`,
			),
		}}
		tg := &migrate.Terragrunt{Runner: r, Bin: "terragrunt", Terraform: "terraform"}
		state := filepath.Join(t.TempDir(), "s.json")
		require.NoError(t, os.WriteFile(state, []byte("{}"), 0o600))
		_, err := tg.Plan(t.Context(), t.TempDir(), state)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "remote_state must use generate")
	})
	t.Run("leftover override", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(dir, migrate.OverrideFile), nil, 0o600))
		tg := &migrate.Terragrunt{Runner: &fakeRunner{t: t}, Bin: "terragrunt", Terraform: "terraform"}
		_, err := tg.Plan(t.Context(), dir, filepath.Join(dir, "s.json"))
		require.ErrorIs(t, err, migrate.ErrLeftover)
		assert.FileExists(t, filepath.Join(dir, migrate.OverrideFile), "a leftover is never deleted")
	})
}

func TestTerragruntPush(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	r := &fakeRunner{t: t}
	tg := &migrate.Terragrunt{Runner: r, Bin: "terragrunt", Terraform: "tofu"}
	require.NoError(t, tg.Push(t.Context(), dir, "/tmp/state.json"))
	require.Len(t, r.calls, 2)
	assert.Equal(t, []string{"run", "--tf-path", "tofu", "--", "state", "push", "/tmp/state.json"},
		r.calls[1].Args)
	assert.NotContains(t, r.calls[1].Args, "-force")
	assert.Equal(t, "tofu", envOf(r.calls[1], "TG_TF_PATH"))
	assert.NoFileExists(t, filepath.Join(dir, ".terraform.lock.hcl"), "a lock file init created is removed")
}
