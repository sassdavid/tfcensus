package codify_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/codify"
	"github.com/sassdavid/tfcensus/internal/execx"
)

type fakeRunner struct {
	out string
	err error
	got []string
}

func (f *fakeRunner) Run(_ context.Context, c execx.Cmd) (*execx.Result, error) {
	f.got = append([]string{c.Name}, c.Args...)
	return &execx.Result{Stdout: []byte(f.out)}, f.err
}

func lockFile(t *testing.T, host, ver string) string {
	t.Helper()
	dir := t.TempDir()
	if host != "" {
		src := `provider "` + host + `/hashicorp/aws" {
  version = "` + ver + `"
}
`
		require.NoError(t, os.WriteFile(filepath.Join(dir, ".terraform.lock.hcl"), []byte(src), 0o600))
	}
	return dir
}

func TestDetect(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, binary, host, out string
		want                    string
		provider                string
		err                     string
	}{
		{
			name: "terraform with lock", binary: "terraform", host: "registry.terraform.io",
			out: `{"terraform_version":"1.12.2"}`, want: "terraform 1.12.2", provider: "6.20.0",
		},
		{
			name: "tofu by lock host", binary: "/usr/local/bin/tofu", host: "registry.opentofu.org",
			out: `{"terraform_version":"1.11.0"}`, want: "opentofu 1.11.0", provider: "6.20.0",
		},
		{name: "no lock file", binary: "terraform", out: `{"terraform_version":"1.14.1"}`, want: "terraform 1.14.1"},
		{
			name: "lock from tofu, binary terraform", binary: "terraform", host: "registry.opentofu.org",
			err: "the lock file is from opentofu",
		},
		{name: "bad output", binary: "terraform", out: "Terraform v1.12.0", err: "read terraform version"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			run := &fakeRunner{out: tc.out}
			tg, err := codify.Detect(t.Context(), run, tc.binary, lockFile(t, tc.host, "6.20.0"))
			if tc.err != "" {
				require.ErrorContains(t, err, tc.err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, tg.String())
			assert.Equal(t, []string{tc.binary, "version", "-json"}, run.got)
			if tc.provider == "" {
				assert.Nil(t, tg.Provider)
			} else {
				assert.Equal(t, tc.provider, tg.Provider.String())
			}
		})
	}

	_, err := codify.Detect(t.Context(), &fakeRunner{err: errors.New("not found")}, "terraform", t.TempDir())
	require.ErrorContains(t, err, "run terraform version")
}
