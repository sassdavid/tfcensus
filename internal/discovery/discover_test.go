package discovery_test

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
)

const (
	reposDir     = "../../testdata/repos"
	recordingDir = "../../testdata/terragrunt/tg-live"
)

// replay answers terragrunt calls from the recordings that scripts/record-terragrunt.sh made.
type replay struct {
	find string // file under recordingDir answering find

	mu    sync.Mutex
	calls [][]string
}

func (r *replay) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	r.mu.Lock()
	r.calls = append(r.calls, c.Args)
	r.mu.Unlock()
	switch c.Args[0] {
	case "find":
		return readResult(filepath.Join(recordingDir, r.find))
	case "stack":
		return &execx.Result{}, nil
	case "render":
		base := filepath.Join(recordingDir, "render", strings.ReplaceAll(c.Args[2], "/", "__"))
		if res, err := readResult(base + ".json"); err == nil {
			return res, nil
		}
		stderr, err := os.ReadFile(base + ".stderr")
		if err != nil {
			return nil, fmt.Errorf("no recording for %s: %w", c.Args[2], err)
		}
		return &execx.Result{Stderr: stderr, ExitCode: 1},
			&execx.ExitError{Name: "terragrunt", Code: 1, Stderr: string(stderr)}
	}
	return nil, fmt.Errorf("unexpected call %q", c.Args)
}

func (r *replay) rendered() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var units []string
	for _, args := range r.calls {
		if args[0] == "render" {
			units = append(units, args[2])
		}
	}
	slices.Sort(units)
	return units
}

func readResult(path string) (*execx.Result, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err //nolint:wrapcheck // test helper
	}
	return &execx.Result{Stdout: b}, nil
}

func discover(t *testing.T, r discovery.Runner, mode discovery.RenderMode, repos ...string) discovery.Result {
	t.Helper()
	d := discovery.New(r, slog.New(slog.DiscardHandler), discovery.Options{Render: mode, WorkDir: reposDir})
	return d.Discover(t.Context(), repos)
}

func byPath(units []discovery.Unit) map[string]discovery.Unit {
	m := map[string]discovery.Unit{}
	for _, u := range units {
		m[u.Path] = u
	}
	return m
}

// The fast path must agree with terragrunt render wherever both resolve a unit.
func TestFastPathMatchesRender(t *testing.T) {
	t.Parallel()

	fast := discover(t, &replay{find: "find.json"}, discovery.RenderNever, "tg-live")
	rendered := discover(t, &replay{find: "find.json"}, discovery.RenderAlways, "tg-live")
	require.Empty(t, fast.Errors)
	require.Empty(t, rendered.Errors)

	both := 0
	renderUnits := byPath(rendered.Units)
	for _, f := range fast.Units {
		r := renderUnits[f.Path]
		if f.Resolution != discovery.MethodFastPath || r.Resolution != discovery.MethodRender {
			continue
		}
		both++
		assert.Equal(t, r.Backend, f.Backend, f.Path)
		assert.Equal(t, r.Target, f.Target, f.Path)
	}
	assert.Equal(t, 10, both, "units resolved by both methods")

	fastUnits := byPath(fast.Units)
	assert.Equal(t, discovery.MethodFastPath, fastUnits["live/prod/eu-central-1/prod/edge"].Resolution,
		"terragrunt evaluates every local; the fast path only the ones remote_state needs")
	assert.Equal(t, discovery.MethodUnresolved, renderUnits["live/prod/eu-central-1/prod/edge"].Resolution)
	assert.Equal(t, discovery.MethodRender, renderUnits["live/prod/eu-west-1/prod/dns"].Resolution)
	assert.Equal(t, discovery.MethodUnresolved, fastUnits["live/prod/eu-west-1/prod/dns"].Resolution)
	assert.Contains(t, fastUnits["live/prod/eu-west-1/prod/dns"].Reason, "run_cmd")
}

func TestAutoRendersOnlyWhatTheFastPathCannot(t *testing.T) {
	t.Parallel()

	r := &replay{find: "find.json"}
	res := discover(t, r, discovery.RenderAuto, "tg-live")
	require.Empty(t, res.Errors)
	assert.Equal(t, []string{"live/prod/eu-west-1/prod/dns", "live/prod/eu-west-1/prod/secrets"}, r.rendered())

	units := byPath(res.Units)
	dns := units["live/prod/eu-west-1/prod/dns"]
	assert.Equal(t, discovery.MethodRender, dns.Resolution)
	assert.Equal(t, "prod/dns/terraform.tfstate", dns.Backend.Key)
	require.Len(t, dns.Warnings, 1)
	assert.Contains(t, dns.Warnings[0], "resolved by render; the fast path failed")

	secrets := units["live/prod/eu-west-1/prod/secrets"]
	assert.Equal(t, discovery.MethodUnresolved, secrets.Resolution)
	assert.Contains(t, secrets.Reason, "fast path: remote_state config bucket:")
	assert.Contains(t, secrets.Reason, `render: render failed: live/prod/eu-west-1/prod/secrets/terragrunt.hcl:14`)
	assert.Nil(t, secrets.Backend)

	assert.True(t, units["live/prod/eu-west-1/prod/legacy"].Excluded)
	assert.Equal(t, discovery.LockBoth, units["live/prod/eu-west-1/prod/db"].Lock)
	stack := units["live/prod/eu-central-1/prod/services"]
	assert.Equal(t, discovery.KindTerragruntStack, stack.Kind)
	assert.Equal(t, []string{"not generated: run with --generate-stacks to list its units"}, stack.Warnings)
}

func TestGenerateStacks(t *testing.T) {
	t.Parallel()

	r := &replay{find: "find-generated.json"}
	d := discovery.New(r, slog.New(slog.DiscardHandler), discovery.Options{GenerateStacks: true, WorkDir: reposDir})
	res := d.Discover(t.Context(), []string{"tg-live"})
	require.Empty(t, res.Errors)
	require.Equal(t, []string{"stack", "generate", "--no-color", "--log-level", "error"}, r.calls[0])

	units := byPath(res.Units)
	assert.Empty(t, units["live/prod/eu-central-1/prod/services"].Warnings)
	api := units["live/prod/eu-central-1/prod/services/.terragrunt-stack/api"]
	// The fake generate writes nothing, so the fast path finds no file and render resolves the unit.
	assert.Equal(t, discovery.MethodRender, api.Resolution)
	assert.Equal(t, "live/prod/eu-central-1/prod/services/.terragrunt-stack/api/terraform.tfstate", api.Backend.Key)
	assert.Equal(t, "eu-central-1", api.Target.Region)
}

func TestRepoErrorsDoNotStopOthers(t *testing.T) {
	t.Parallel()

	res := discover(t, &replay{find: "find.json"}, discovery.RenderNever, "missing", "plain-tf")
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "missing", res.Errors[0].Repo)
	assert.Len(t, res.Units, 3)
}

// unitsRunner answers find with the given unit paths and fails everything else.
type unitsRunner []string

func (u unitsRunner) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	if c.Args[0] != "find" {
		return nil, fmt.Errorf("unexpected call %q", c.Args)
	}
	items := make([]string, len(u))
	for i, p := range u {
		items[i] = fmt.Sprintf(`{"type":"unit","path":%q}`, p)
	}
	return &execx.Result{Stdout: []byte("[" + strings.Join(items, ",") + "]")}, nil
}

func writeTree(t testing.TB, root string, files map[string]string) {
	t.Helper()
	for name, content := range files {
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o750))
		require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	}
}

const rootWithState = `
remote_state {
  backend = "s3"
  config = {
    bucket = "state"
    key    = "${path_relative_to_include()}/terraform.tfstate"
    region = "eu-west-1"
  }
}
`

func TestFastPath(t *testing.T) {
	t.Parallel()

	include := "include \"root\" {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n"
	tests := []struct {
		name       string
		files      map[string]string
		wantKey    string
		wantBucket string
		wantReason string
		wantLock   discovery.Lock
	}{
		{
			name:    "path_relative_to_include",
			files:   map[string]string{"root.hcl": rootWithState, "a/b/terragrunt.hcl": include},
			wantKey: "a/b/terraform.tfstate", wantBucket: "state", wantLock: discovery.LockNone,
		},
		{
			name: "remote_state as an attribute",
			files: map[string]string{"u/terragrunt.hcl": `remote_state = {
  backend = "s3"
  config  = { bucket = "b", key = "k", region = "r", use_lockfile = true }
}`},
			wantKey: "k", wantBucket: "b", wantLock: discovery.LockLockfile,
		},
		{
			name: "deep merge keeps the include's keys",
			files: map[string]string{
				"root.hcl": rootWithState,
				"u/terragrunt.hcl": `include "root" {
  path           = find_in_parent_folders("root.hcl")
  merge_strategy = "deep"
}
remote_state {
  backend = "s3"
  config  = { dynamodb_table = "locks" }
}`,
			},
			wantKey: "u/terraform.tfstate", wantBucket: "state", wantLock: discovery.LockDynamoDB,
		},
		{
			name: "an unused local that fails doesn't matter",
			files: map[string]string{"u/terragrunt.hcl": `locals {
  who    = run_cmd("whoami")
  bucket = "b-${lower("X")}"
}
remote_state {
  backend = "s3"
  config  = { bucket = local.bucket, key = "k", region = "r" }
}`},
			wantKey: "k", wantBucket: "b-x",
		},
		{
			name: "a used local that fails gives its reason",
			files: map[string]string{"u/terragrunt.hcl": `locals {
  acct = get_aws_account_id()
}
remote_state {
  backend = "s3"
  config  = { bucket = "b-${local.acct}", key = "k", region = "r" }
}`},
			wantReason: "fast path: remote_state config bucket: local.acct: u/terragrunt.hcl:2: Error in function call",
		},
		{
			name: "account.hcl, get_env and functions",
			files: map[string]string{
				"live/account.hcl": `locals { name = "acme" }`,
				"live/u/terragrunt.hcl": `locals {
  acct   = read_terragrunt_config(find_in_parent_folders("account.hcl"))
  region = read_terragrunt_config("missing.hcl", { locals = { r = "eu-west-1" } }).locals.r
}
remote_state {
  backend = "s3"
  config  = {
    bucket = "${local.acct.locals.name}-${get_env("TFCENSUS_TEST_UNSET", "dflt")}"
    key    = "${replace(basename(get_terragrunt_dir()), "/^u$/", "unit")}-${length("ab")}"
    region = local.region
  }
}`,
			},
			wantKey: "unit-2", wantBucket: "acme-dflt",
		},
		{
			name: "values from a generated stack unit",
			files: map[string]string{
				"s/terragrunt.values.hcl": `name = "api"`,
				"s/terragrunt.hcl": `remote_state {
  backend = "s3"
  config  = { bucket = "b", key = "${values.name}.tfstate", region = "r" }
}`,
			},
			wantKey: "api.tfstate", wantBucket: "b",
		},
		{
			name:       "no remote_state",
			files:      map[string]string{"u/terragrunt.hcl": `inputs = {}`},
			wantReason: "fast path: no remote_state block in the unit or its includes",
		},
		{
			name: "partial configuration",
			files: map[string]string{"u/terragrunt.hcl": `remote_state {
  backend = "s3"
  config  = { region = "r" }
}`},
			wantReason: "fast path: backend config has no bucket or key (partial configuration, set at init time)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			root := t.TempDir()
			writeTree(t, root, tt.files)
			var unitPath string
			for name := range tt.files {
				if dir, ok := strings.CutSuffix(name, "/terragrunt.hcl"); ok {
					unitPath = dir
				}
			}
			d := discovery.New(unitsRunner{unitPath}, slog.New(slog.DiscardHandler),
				discovery.Options{Render: discovery.RenderNever})
			res := d.Discover(t.Context(), []string{root})
			require.Empty(t, res.Errors)
			require.Len(t, res.Units, 1)
			u := res.Units[0]
			if tt.wantReason != "" {
				assert.Equal(t, discovery.MethodUnresolved, u.Resolution)
				assert.Contains(t, u.Reason, tt.wantReason)
				return
			}
			require.Equal(t, discovery.MethodFastPath, u.Resolution, u.Reason)
			assert.Equal(t, tt.wantKey, u.Backend.Key)
			assert.Equal(t, tt.wantBucket, u.Backend.Bucket)
			if tt.wantLock != "" {
				assert.Equal(t, tt.wantLock, u.Lock)
			}
		})
	}
}

func TestParseRenderMode(t *testing.T) {
	t.Parallel()

	for _, s := range []string{"auto", "always", "never"} {
		m, err := discovery.ParseRenderMode(s)
		require.NoError(t, err)
		assert.Equal(t, discovery.RenderMode(s), m)
	}
	_, err := discovery.ParseRenderMode("sometimes")
	require.ErrorContains(t, err, `invalid render mode "sometimes"`)
}

// generateTree writes n units in the account/region/env layout of tg-live and returns their paths.
func generateTree(t testing.TB, root string, n int) []string {
	t.Helper()
	src, err := os.ReadFile(filepath.Join(reposDir, "tg-live", "root.hcl"))
	require.NoError(t, err)
	files := map[string]string{"root.hcl": string(src)}
	units := make([]string, 0, n)
	for i := range n {
		acct := fmt.Sprintf("acct%d", i%5)
		region := fmt.Sprintf("%s/region%d", acct, i%3)
		env := fmt.Sprintf("%s/env%d", region, i%4)
		files[acct+"/account.hcl"] = fmt.Sprintf(
			"locals {\n  account_name = %q\n  aws_account_id = \"%012d\"\n}\n",
			acct,
			i%5,
		)
		files[region+"/region.hcl"] = "locals {\n  aws_region = \"eu-west-1\"\n}\n"
		files[env+"/env.hcl"] = "locals {\n  env = \"e\"\n}\n"
		unit := fmt.Sprintf("%s/unit%03d", env, i)
		files[unit+"/terragrunt.hcl"] = "include \"root\" {\n  path = find_in_parent_folders(\"root.hcl\")\n}\n"
		units = append(units, unit)
	}
	writeTree(t, root, files)
	return units
}

// TestSpec_discovery_5: 300 units resolve in under 10 s by the fast path, with no exec.
func TestSpec_discovery_5(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	units := generateTree(t, root, 300)
	d := discovery.New(
		unitsRunner(units),
		slog.New(slog.DiscardHandler),
		discovery.Options{Render: discovery.RenderNever},
	)
	start := time.Now()
	res := d.Discover(t.Context(), []string{root})
	elapsed := time.Since(start)
	require.Empty(t, res.Errors)
	require.Len(t, res.Units, 300)
	for _, u := range res.Units {
		require.Equal(t, discovery.MethodFastPath, u.Resolution, u.Reason)
	}
	assert.Less(t, elapsed, 10*time.Second)
	t.Logf("300 units resolved in %s", elapsed)
}

func BenchmarkDiscover300Units(b *testing.B) {
	root := b.TempDir()
	units := generateTree(b, root, 300)
	d := discovery.New(
		unitsRunner(units),
		slog.New(slog.DiscardHandler),
		discovery.Options{Render: discovery.RenderNever},
	)
	for b.Loop() {
		if res := d.Discover(b.Context(), []string{root}); len(res.Units) != 300 {
			b.Fatalf("got %d units", len(res.Units))
		}
	}
}
