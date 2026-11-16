package config_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v4"

	"github.com/sassdavid/tfcensus/internal/config"
)

const schemaLine = "schema_version: 1\n"

// uncomment removes the "# " in front of every line after schema_version, as a user who
// uncomments every key would.
func uncomment(t *testing.T, b []byte) string {
	t.Helper()
	head, body, ok := strings.Cut(string(b), schemaLine)
	require.True(t, ok, "the template has no %q line", strings.TrimSpace(schemaLine))
	lines := strings.Split(body, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimPrefix(strings.TrimPrefix(l, "#"), " ")
	}
	return head + schemaLine + strings.Join(lines, "\n")
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return string(b)
}

// TestSpec_config_4 checks that the config init template is valid, changes nothing as
// written, and sets every key to its default once uncommented.
func TestSpec_config_4(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	defaults, err := config.Load(config.Options{Environ: env(dir), Cwd: dir})
	require.NoError(t, err)

	commented := filepath.Join(dir, "commented.yaml")
	writeFile(t, commented, string(config.Template()))
	require.NoError(t, config.ValidateFile(commented))
	res, err := config.Load(config.Options{Environ: env(dir), Cwd: dir, ExplicitFile: commented})
	require.NoError(t, err)
	assert.Equal(t, defaults.Config, res.Config)
	for _, k := range res.Keys() {
		if k != "schema_version" {
			assert.Equal(t, config.SourceDefault, res.Source(k).Kind, "%s is set in the template", k)
		}
	}

	text := uncomment(t, config.Template())
	uncommented := filepath.Join(dir, "uncommented.yaml")
	writeFile(t, uncommented, text)
	require.NoError(t, config.ValidateFile(uncommented))
	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(text), &m))
	assert.JSONEq(
		t,
		toJSON(t, defaults.Raw()),
		toJSON(t, m),
		"the uncommented template must list every key at its default",
	)
}

func TestWriteTemplate(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "new", "dir", "config.yaml")
	require.NoError(t, config.WriteTemplate(path, false))
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, config.Template(), got)

	writeFile(t, path, "kept\n")
	err = config.WriteTemplate(path, false)
	require.ErrorIs(t, err, config.ErrExists)
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, "kept\n", string(got))

	require.NoError(t, config.WriteTemplate(path, true))
	got, err = os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, config.Template(), got)

	err = config.WriteTemplate(dir, true)
	require.Error(t, err)
	assert.NotErrorIs(t, err, config.ErrExists)
}
