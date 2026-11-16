package tfstate_test

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/discovery"
	"github.com/sassdavid/tfcensus/internal/execx"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
	"github.com/sassdavid/tfcensus/internal/tfstate"
)

const tofuKeys = `key_provider "pbkdf2" "main" { passphrase = "FIXTURE-SECRET-passphrase" }`

func recordedEnv() []string {
	return []string{
		"TF_DATA_DIR", "TF_IN_AUTOMATION", "TF_INPUT", "TF_WORKSPACE", "TF_ENCRYPTION",
		"TG_TF_PATH", "TG_DOWNLOAD_DIR", "TG_NO_AUTO_INIT", "TG_NON_INTERACTIVE",
	}
}

// showRules answer init and show -json with the recorded output, after prefix.
func showRules(t *testing.T, prefix ...string) []fakebin.Rule {
	t.Helper()
	show, err := filepath.Abs(filepath.Join(tofuDir, "show.json"))
	require.NoError(t, err)
	return []fakebin.Rule{
		{Args: append(append([]string{}, prefix...), "init", "-input=false", "-no-color", "-lock=false")},
		{Args: append(append([]string{}, prefix...), "show", "-json", "-no-color"), StdoutFile: show},
	}
}

func newTofu(tofu, terragrunt string) *tfstate.Tofu {
	env := func() []string { return []string{"PATH=" + os.Getenv("PATH"), "TF_ENCRYPTION=" + tofuKeys} }
	return &tfstate.Tofu{
		Runner:     execx.NewRunner(slog.New(slog.DiscardHandler), execx.WithEnviron(env)),
		Bin:        tofu,
		Terragrunt: terragrunt,
	}
}

// TestTofuDecrypt runs the fake tofu directly for a plain root module and through the fake
// terragrunt for a terragrunt unit, in the unit's directory, with the key configuration from the
// environment and a temporary TF_DATA_DIR.
func TestTofuDecrypt(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		kind      discovery.Kind
		workspace string
	}{
		{discovery.KindTerraform, tfstate.DefaultWorkspace},
		{discovery.KindTerraform, "staging"},
		{discovery.KindTerragrunt, tfstate.DefaultWorkspace},
	} {
		t.Run(string(tt.kind)+"-"+tt.workspace, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			tofu, tg := "tofu", "terragrunt"
			var bin string
			if tt.kind == discovery.KindTerragrunt {
				bin = fakebin.Install(t, "terragrunt", fakebin.Spec{
					Rules: showRules(t, "run", "--tf-path", tofu, "--"), RecordEnv: recordedEnv(),
				})
				tg = bin
			} else {
				bin = fakebin.Install(t, "tofu", fakebin.Spec{Rules: showRules(t), RecordEnv: recordedEnv()})
				tofu = bin
			}
			out, err := newTofu(tofu, tg).Decrypt(t.Context(), &discovery.Unit{Kind: tt.kind}, dir, tt.workspace)
			require.NoError(t, err)
			assert.Equal(t, readTofu(t, "show.json"), out)

			calls := fakebin.Calls(t, bin)
			require.Len(t, calls, 2)
			want := []string{"init", "show"}
			for i, c := range calls {
				assert.Contains(t, c.Args, want[i])
				realDir, evalErr := filepath.EvalSymlinks(dir)
				require.NoError(t, evalErr)
				assert.Equal(t, realDir, c.Dir)
				assert.Equal(t, tofuKeys, c.Env["TF_ENCRYPTION"], "the key configuration passes through")
				assert.NotEmpty(t, c.Env["TF_DATA_DIR"])
				assert.NotContains(t, c.Env["TF_DATA_DIR"], dir, "the unit's .terraform stays as it was")
				assert.Equal(t, "1", c.Env["TF_IN_AUTOMATION"])
				if tt.workspace == tfstate.DefaultWorkspace {
					assert.NotContains(t, c.Env, "TF_WORKSPACE")
				} else {
					assert.Equal(t, tt.workspace, c.Env["TF_WORKSPACE"])
				}
				if tt.kind == discovery.KindTerragrunt {
					assert.Equal(t, tofu, c.Env["TG_TF_PATH"])
					assert.Equal(t, "true", c.Env["TG_NO_AUTO_INIT"])
					assert.NotEmpty(t, c.Env["TG_DOWNLOAD_DIR"])
				}
			}
			_, err = os.Stat(calls[0].Env["TF_DATA_DIR"])
			require.ErrorIs(t, err, os.ErrNotExist, "the temp dir is removed")
			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			assert.Empty(t, entries, "nothing is left in the unit")
		})
	}
}

// TestTofuDecryptErrors checks that each failure has a fixed message without tofu's output.
func TestTofuDecryptErrors(t *testing.T) {
	t.Parallel()

	wrongKey := string(readTofu(t, "show-wrong-key.stderr"))
	wrongCode, err := strconv.Atoi(strings.TrimSpace(string(readTofu(t, "show-wrong-key.exit"))))
	require.NoError(t, err)
	leak := "Error: FIXTURE-SECRET-db-password in the backend config"
	plain := &discovery.Unit{Kind: discovery.KindTerraform}
	tests := []struct {
		name  string
		rules []fakebin.Rule
		tofu  string
		tg    string
		unit  *discovery.Unit
		want  string
	}{
		{
			name: "init fails",
			rules: []fakebin.Rule{
				{Args: []string{"init"}, Stderr: leak, ExitCode: 1},
			},
			unit: plain,
			want: "decrypt state with tofu: tofu init exited with code 1; check the unit's backend configuration " +
				"and its credentials (run it in the unit to see why)",
		},
		{
			name: "wrong key",
			rules: []fakebin.Rule{
				{Args: []string{"init"}},
				{Args: []string{"show"}, Stderr: wrongKey, ExitCode: wrongCode},
			},
			unit: plain,
			want: "decrypt state with tofu: tofu show -json exited with code 1; check the key configuration in " +
				"the unit's encryption block or TF_ENCRYPTION (run it in the unit to see why)",
		},
		{
			name: "tofu missing",
			tofu: filepath.Join("no", "such", "tofu"),
			unit: plain,
			want: "decrypt state with tofu: " + filepath.Join("no", "such", "tofu") +
				" not found; install OpenTofu or set --tofu-path (config binaries.tofu)",
		},
		{
			name: "terragrunt missing",
			tg:   filepath.Join("no", "such", "terragrunt"),
			unit: &discovery.Unit{Kind: discovery.KindTerragrunt},
			want: "decrypt state with tofu: " + filepath.Join("no", "such", "terragrunt") +
				" not found; install it or set --tg-path (config binaries.terragrunt)",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tofu := tt.tofu
			if tofu == "" {
				tofu = fakebin.Install(t, "tofu", fakebin.Spec{Rules: tt.rules})
			}
			_, decErr := newTofu(tofu, tt.tg).Decrypt(t.Context(), tt.unit, t.TempDir(), tfstate.DefaultWorkspace)
			require.ErrorIs(t, decErr, tfstate.ErrDecrypt)
			assert.Equal(t, tt.want, decErr.Error())
			_, isExit := errors.AsType[*execx.ExitError](decErr)
			assert.False(t, isExit, "the exit error, which holds stderr, is dropped")
		})
	}
}

// TestKeepLockFile checks that init can't change or add the unit's lock file.
func TestKeepLockFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lock := filepath.Join(dir, tfstate.LockFileName)
	restore, err := tfstate.KeepLockFile(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, []byte("created"), 0o600))
	require.NoError(t, restore())
	assert.NoFileExists(t, lock)

	require.NoError(t, os.WriteFile(lock, []byte("original"), 0o600))
	restore, err = tfstate.KeepLockFile(dir)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(lock, []byte("changed"), 0o600))
	require.NoError(t, restore())
	b, err := os.ReadFile(lock)
	require.NoError(t, err)
	assert.Equal(t, "original", string(b))
}
