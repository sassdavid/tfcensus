package complete_test

import (
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/complete"
)

const awsConfig = `[default]
region = eu-central-1

[profile acme-dev]
sso_session = acme

[sso-session acme]
sso_region = eu-central-1

[services local]
s3 =
  endpoint_url = http://localhost:4566

[ profile acme-prod ]
# [profile commented]
[profile acme-dev]
`

func TestProfiles(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config")
	require.NoError(t, os.WriteFile(cfg, []byte(awsConfig), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, ".aws"), 0o750))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".aws", "config"), []byte("[profile home]\n"), 0o600))
	// The credentials file is never read, so its profiles never show.
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".aws", "credentials"), []byte("[secret]\n"), 0o600))

	tests := []struct {
		name string
		env  map[string]string
		want []string
	}{
		{
			"AWS_CONFIG_FILE",
			map[string]string{"AWS_CONFIG_FILE": cfg, "HOME": dir},
			[]string{"acme-dev", "acme-prod", "default"},
		},
		{"home", map[string]string{"HOME": dir}, []string{"home"}},
		{"USERPROFILE", map[string]string{"USERPROFILE": dir}, []string{"home"}},
		{"missing file", map[string]string{"AWS_CONFIG_FILE": filepath.Join(dir, "nope")}, nil},
		{"no home", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			lookup := func(k string) (string, bool) { v, ok := tt.env[k]; return v, ok }
			cs, err := complete.Profiles(lookup)(t.Context(), &cobra.Command{}, "")
			require.NoError(t, err)
			assert.Equal(t, tt.want, valuesOf(cs))
		})
	}
}

func TestUnits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	index := `{"schema_version":"1","data":{"units":[
		{"unit":"live/b","workspace":"default","status":"ok","instances":2},
		{"unit":"live/a","workspace":"blue","status":"absent","instances":0},
		{"unit":"live/a","workspace":"default","status":"ok","instances":1}]}}`
	plain := filepath.Join(dir, "index.json")
	require.NoError(t, os.WriteFile(plain, []byte(index), 0o600))
	gz := filepath.Join(dir, "index.json.gz")
	f, err := os.Create(gz)
	require.NoError(t, err)
	w := gzip.NewWriter(f)
	_, err = w.Write([]byte(index))
	require.NoError(t, err)
	require.NoError(t, w.Close())
	require.NoError(t, f.Close())

	for _, path := range []string{plain, gz} {
		cmd := &cobra.Command{}
		cmd.Flags().String("index", path, "")
		cs, uerr := complete.Units("index")(t.Context(), cmd, "")
		require.NoError(t, uerr)
		assert.Equal(t, []complete.Choice{
			{Value: "live/a", Desc: "absent, 0 instances, workspace blue"},
			{Value: "live/b", Desc: "ok, 2 instances"},
		}, cs, path)
	}

	cmd := &cobra.Command{}
	cmd.Flags().String("index", "", "")
	cs, err := complete.Units("index")(t.Context(), cmd, "")
	require.NoError(t, err)
	assert.Empty(t, cs, "no --index lists nothing")

	require.NoError(t, cmd.Flags().Set("index", filepath.Join(dir, "missing.json")))
	_, err = complete.Units("index")(t.Context(), cmd, "")
	require.Error(t, err)
}

func TestTypes(t *testing.T) {
	t.Parallel()
	cmd := &cobra.Command{}
	tf, err := complete.Types()(t.Context(), cmd, "aws_iam_r")
	require.NoError(t, err)
	assert.Contains(t, tf, complete.Choice{Value: "aws_iam_role", Desc: "AWS::IAM::Role"})

	cfn, err := complete.Types()(t.Context(), cmd, "AWS::IAM::")
	require.NoError(t, err)
	assert.Contains(t, cfn, complete.Choice{Value: "AWS::IAM::Role", Desc: "aws_iam_role"})
	assert.Contains(t, cfn, complete.Choice{Value: "AWS::IAM::*", Desc: "every IAM type"})
	for _, c := range cfn {
		assert.False(t, strings.HasPrefix(c.Value, "aws_"), c.Value)
	}
}

func TestRegions(t *testing.T) {
	t.Parallel()
	seen := map[string]bool{}
	for _, r := range complete.Regions() {
		assert.False(t, seen[r.Value], "duplicate %s", r.Value)
		seen[r.Value] = true
		assert.NotEmpty(t, r.Desc, r.Value)
	}
	assert.True(t, seen["eu-central-1"])
}

func valuesOf(cs []complete.Choice) []string {
	if cs == nil {
		return nil
	}
	return complete.Values(cs)
}
