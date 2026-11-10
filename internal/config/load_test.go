package config_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/config"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
}

// env returns an isolated environment rooted at dir.
func env(dir string, extra ...string) []string {
	return append([]string{
		"HOME=" + filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME=" + filepath.Join(dir, "xdg"),
	}, extra...)
}

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := config.Load(config.Options{Environ: env(dir), Cwd: dir})
	require.NoError(t, err)
	assert.Equal(t, config.Default(), res.Config)
	assert.Equal(t, config.SourceDefault, res.Source("audit.policy.noncurrent_days").Kind)
	assert.Contains(t, res.Keys(), "discovery.generate_stacks")
	assert.Empty(t, res.UnknownEnv)
}

func TestLoad_Precedence(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "xdg", "tfcensus", "config.yaml"), `
audit: {policy: {noncurrent_days: 2, min_terraform_version: "1.11"}}
binaries: {terragrunt: /user/terragrunt}
cache: {ttl: 2h}
accounts: {map: {"111111111111": {profile: a}}}
`)
	writeFile(t, filepath.Join(dir, ".tfcensus.yaml"), `
audit: {policy: {noncurrent_days: 3}}
binaries: {terraform: tofu}
accounts: {map: {"222222222222": {profile: b}}}
`)
	fs := pflag.NewFlagSet("test", pflag.ContinueOnError)
	fs.String("tf-path", "terraform", "")
	fs.String("tg-path", "terragrunt", "") // not changed: must not override lower layers
	require.NoError(t, fs.Parse([]string{"--tf-path=/opt/terraform"}))

	res, err := config.Load(config.Options{
		Environ: env(dir, "TFCENSUS_AUDIT_POLICY_NONCURRENT_DAYS=4", "TFCENSUS_BINARIES_TERRAFORM=env-tofu"),
		Cwd:     dir,
		Flags:   fs,
		FlagKeys: map[string]string{
			"tf-path": "binaries.terraform",
			"tg-path": "binaries.terragrunt",
		},
	})
	require.NoError(t, err)

	c := res.Config
	assert.Equal(t, "1.11", c.Audit.Policy.MinTerraformVersion, "user file")
	assert.Equal(t, "/user/terragrunt", c.Binaries.Terragrunt, "user file, unchanged flag ignored")
	assert.Equal(t, "2h", c.Cache.TTL, "user file")
	assert.Equal(t, 4, c.Audit.Policy.NoncurrentDays, "env beats project")
	assert.Equal(t, "/opt/terraform", c.Binaries.Terraform, "flag beats env")
	assert.Equal(t, map[string]config.AccountRef{"111111111111": {Profile: "a"}, "222222222222": {Profile: "b"}},
		c.Accounts.Map, "maps merge across files")

	userFile := filepath.Join(dir, "xdg", "tfcensus", "config.yaml")
	projectFile := filepath.Join(dir, ".tfcensus.yaml")
	assert.Equal(t, config.Source{Kind: config.SourceUser, Name: userFile}, res.Source("binaries.terragrunt"))
	assert.Equal(
		t,
		config.Source{Kind: config.SourceUser, Name: userFile},
		res.Source("accounts.map.111111111111.profile"),
	)
	assert.Equal(t, config.Source{Kind: config.SourceProject, Name: projectFile},
		res.Source("accounts.map.222222222222.profile"))
	assert.Equal(t, config.Source{Kind: config.SourceEnv, Name: "TFCENSUS_AUDIT_POLICY_NONCURRENT_DAYS"},
		res.Source("audit.policy.noncurrent_days"))
	assert.Equal(t, config.Source{Kind: config.SourceFlag, Name: "tf-path"}, res.Source("binaries.terraform"))
	assert.Equal(t, "flag --tf-path", res.Source("binaries.terraform").String())
}

func TestLoad_EnvCoercion(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	res, err := config.Load(config.Options{
		Environ: env(dir,
			"TFCENSUS_REGIONS= eu-west-1 , eu-central-1 ",
			"TFCENSUS_ACCOUNTS_ORG_ENABLED=1",
			"TFCENSUS_DISCOVERY__GENERATE_STACKS=true",
			"TFCENSUS_SCAN_PACKS_AWS_DEFAULTS=false",
			"TFCENSUS_AUDIT_POLICY_STALE_LOCK_HOURS=", // empty means unset for numbers
			"TFCENSUS_TYPO=1",
			"TFCENSUS_CONFIG=",
		),
		Cwd: dir,
	})
	require.NoError(t, err)
	assert.Equal(t, []string{"eu-west-1", "eu-central-1"}, res.Config.Regions)
	assert.True(t, res.Config.Accounts.Org.Enabled)
	assert.True(t, res.Config.Discovery.GenerateStacks)
	assert.False(t, res.Config.Scan.Packs.AWSDefaults, "hyphenated keys map to _ in env names")
	assert.Equal(t, config.Default().Audit.Policy.StaleLockHours, res.Config.Audit.Policy.StaleLockHours)
	assert.Equal(t, []string{"TFCENSUS_TYPO"}, res.UnknownEnv)
}

func TestLoad_Errors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		file    string
		environ []string
		want    string
	}{
		{
			name: "unknown key",
			file: "binaries:\n  terrafrom: x\n",
			want: ".tfcensus.yaml:2:3: binaries.terrafrom: unknown key",
		},
		{
			name: "wrong type",
			file: "discovery:\n  roots: /src\n",
			want: ".tfcensus.yaml:2:10: discovery.roots: wrong type: got string, want array",
		},
		{
			name: "top level not a mapping",
			file: "- a\n- b\n",
			want: ".tfcensus.yaml:1:1: wrong type: got array, want object",
		},
		{
			name: "non-string keys",
			file: "1: a\n",
			want: ".tfcensus.yaml:1:1: 1: unknown key",
		},
		{
			name:    "env bool",
			environ: []string{"TFCENSUS_DISCOVERY_GENERATE_STACKS=yes please"},
			want:    `env TFCENSUS_DISCOVERY_GENERATE_STACKS: discovery.generate_stacks: wrong type: "yes please" is not a boolean`,
		},
		{
			name:    "env enum",
			environ: []string{"TFCENSUS_DRIFT_MODE=never"},
			want:    "env TFCENSUS_DRIFT_MODE: drift.mode: value must be one of",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tt.file != "" {
				writeFile(t, filepath.Join(dir, ".tfcensus.yaml"), tt.file)
			}
			_, err := config.Load(config.Options{Environ: env(dir, tt.environ...), Cwd: dir})
			require.Error(t, err)
			require.ErrorIs(t, err, config.ErrInvalid)
			var ve *config.ValidationError
			require.ErrorAs(t, err, &ve)
			assert.Contains(t, err.Error(), tt.want)
		})
	}
}

func TestLocate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	deep := filepath.Join(repo, "a", "b")
	require.NoError(t, os.MkdirAll(deep, 0o750))
	require.NoError(t, os.Mkdir(filepath.Join(repo, ".git"), 0o750))
	// Above the git root: must not be found from inside the repo.
	writeFile(t, filepath.Join(dir, ".tfcensus.yaml"), "{}\n")

	files, err := config.Locate(config.Options{Environ: env(dir), Cwd: deep})
	require.NoError(t, err)
	require.Len(t, files, 2)
	assert.Equal(t, filepath.Join(dir, "xdg", "tfcensus", "config.yaml"), files[0].Path)
	assert.False(t, files[0].Found)
	assert.Equal(t, config.File{Kind: config.SourceProject}, files[1])

	writeFile(t, filepath.Join(repo, "a", ".tfcensus.yaml"), "{}\n")
	files, err = config.Locate(config.Options{Environ: env(dir), Cwd: deep})
	require.NoError(t, err)
	assert.Equal(
		t,
		config.File{Kind: config.SourceProject, Path: filepath.Join(repo, "a", ".tfcensus.yaml"), Found: true},
		files[1],
	)

	missing := filepath.Join(dir, "nope.yaml")
	files, err = config.Locate(config.Options{Environ: env(dir, "TFCENSUS_CONFIG="+missing), Cwd: deep})
	require.NoError(t, err)
	assert.Equal(t, config.File{Kind: config.SourceProject, Path: missing, Explicit: true}, files[1])

	_, err = config.Load(config.Options{Environ: env(dir), Cwd: deep, ExplicitFile: missing})
	require.ErrorIs(t, err, config.ErrInvalid)
}

func TestUserConfigPath(t *testing.T) {
	t.Parallel()
	lookup := func(m map[string]string) config.LookupFunc {
		return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
	}
	tests := []struct {
		name string
		env  map[string]string
		goos string
		want string
	}{
		{"xdg wins", map[string]string{"XDG_CONFIG_HOME": "/x", "HOME": "/h"}, "darwin", "/x/tfcensus/config.yaml"},
		{"macOS uses ~/.config", map[string]string{"HOME": "/h"}, "darwin", "/h/.config/tfcensus/config.yaml"},
		{
			"relative xdg ignored",
			map[string]string{"XDG_CONFIG_HOME": "rel", "HOME": "/h"},
			"linux",
			"/h/.config/tfcensus/config.yaml",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if filepath.Separator != '/' {
				t.Skip("unix paths")
			}
			got, err := config.UserConfigPath(lookup(tt.env), tt.goos)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestValidateFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	good := filepath.Join(dir, "good.yaml")
	writeFile(t, good, "# only comments\n")
	require.NoError(t, config.ValidateFile(good))

	err := config.ValidateFile(filepath.Join(dir, "missing.yaml"))
	require.ErrorIs(t, err, config.ErrInvalid)
	assert.NotErrorAs(t, err, new(*config.ValidationError))
}

func TestEnvVarName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "TFCENSUS_DISCOVERY_GENERATE_STACKS", config.EnvVarName("discovery.generate_stacks"))
	assert.Equal(t, "TFCENSUS_SCAN_PACKS_AWS_DEFAULTS", config.EnvVarName("scan.packs.aws-defaults"))
}
