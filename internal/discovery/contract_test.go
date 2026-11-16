//go:build contract

package discovery_test

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
)

// TestTerragruntContract runs the terragrunt on PATH against tg-live and checks that discovery
// reads the same units from it as from the recordings in testdata/terragrunt/tg-live. A failure
// means terragrunt changed a field tfcensus reads: re-record with scripts/record-terragrunt.sh and
// review the diff. Every AWS credential source is blanked, so nothing can reach AWS.
func TestTerragruntContract(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath("terragrunt"); err != nil {
		t.Fatal("the contract test needs terragrunt on PATH (MISE_ENV=e2e mise exec)")
	}
	tests := []struct {
		name string
		find string
		opts discovery.Options
	}{
		{
			name: "find and render every unit",
			find: "find.json",
			opts: discovery.Options{Render: discovery.RenderAlways},
		},
		{
			name: "filter",
			find: "find-prod.json",
			opts: discovery.Options{Render: discovery.RenderAlways, Filters: []string{"./live/prod/**"}},
		},
		{
			name: "generated stacks",
			find: "find-generated.json",
			opts: discovery.Options{Render: discovery.RenderAlways, GenerateStacks: true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			recorded := tt.opts
			recorded.WorkDir = reposDir
			want := discovery.New(&replay{find: tt.find}, slog.New(slog.DiscardHandler), recorded).
				Discover(t.Context(), []string{"tg-live"})

			work := fixtureCopy(t)
			env := []string{
				"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "TMPDIR=" + t.TempDir(),
				"AWS_CONFIG_FILE=" + os.DevNull, "AWS_SHARED_CREDENTIALS_FILE=" + os.DevNull,
				"AWS_EC2_METADATA_DISABLED=true",
			}
			runner := execx.NewRunner(slog.New(slog.DiscardHandler), execx.WithEnviron(func() []string { return env }))
			live := tt.opts
			live.WorkDir = work
			got := discovery.New(runner, slog.New(slog.DiscardHandler), live).Discover(t.Context(), []string{"tg-live"})

			normalize(&got, work)
			assert.Equal(t, errorStrings(want.Errors), errorStrings(got.Errors))
			assert.Equal(t, want.Units, got.Units)
		})
	}
}

// fixtureCopy copies tg-live and the catalog its stacks use into a symlink-resolved temp directory,
// because terragrunt relativizes paths against the resolved directory.
func fixtureCopy(t *testing.T) string {
	t.Helper()
	work, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	for _, name := range []string{"tg-live", "tg-catalog"} {
		require.NoError(t, os.CopyFS(filepath.Join(work, name), os.DirFS(filepath.Join(reposDir, name))))
	}
	return work
}

// normalize rewrites temp paths in messages the way scripts/record-terragrunt.sh does.
func normalize(res *discovery.Result, work string) {
	r := strings.NewReplacer(work+"/tg-live/", "", work, "/fixture")
	for i := range res.Units {
		u := &res.Units[i]
		u.Reason = r.Replace(u.Reason)
		for j := range u.Warnings {
			u.Warnings[j] = r.Replace(u.Warnings[j])
		}
	}
}

func errorStrings(errs []*discovery.RepoError) []string {
	out := make([]string, 0, len(errs))
	for _, e := range errs {
		out = append(out, e.Error())
	}
	return out
}
