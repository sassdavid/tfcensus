package cli_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/schemas"
)

func newDeps(t *testing.T) (*app.Deps, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	return &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env:    app.Env{"HOME=" + dir, "XDG_CONFIG_HOME=" + dir},
		Clock:  func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) },
		Getwd:  func() (string, error) { return dir, nil },
	}, &stdout, &stderr
}

// M0 check: `tfcensus version --format json` validates against schemas/report-v1.json.
func TestVersionJSONMatchesReportSchema(t *testing.T) {
	t.Parallel()
	d, stdout, stderr := newDeps(t)
	code := cli.Execute(t.Context(), d, []string{"version", "--format", "json"})
	require.Equal(t, cli.ExitOK, code, stderr.String())
	assert.Empty(t, stderr.String())
	require.NoError(t, validateJSON(schemas.ReportV1, stdout.Bytes()))

	var env struct {
		GeneratedAt string            `json:"generated_at"`
		Data        map[string]string `json:"data"`
	}
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &env))
	assert.Equal(t, "2026-11-10T12:00:00Z", env.GeneratedAt, "uses the injected clock")
	for _, k := range []string{"version", "commit", "date", "go_version", "platform"} {
		assert.NotEmpty(t, env.Data[k], k)
	}
}

func TestReportSchemaRejectsBadEnvelope(t *testing.T) {
	t.Parallel()
	bad := []string{
		`{}`,
		`{"schema_version":"2","tool":"tfcensus","version":"1","generated_at":"2026-01-01T00:00:00Z","data":{},"errors":[]}`,
		`{"schema_version":"1","tool":"tfcensus","version":"1","generated_at":"yesterday","data":{},"errors":[]}`,
		`{"schema_version":"1","tool":"tfcensus","version":"1","generated_at":"2026-01-01T00:00:00Z","data":{},"errors":[],"x":1}`,
	}
	for _, b := range bad {
		assert.Error(t, validateJSON(schemas.ReportV1, []byte(b)), b)
	}
}

func TestExitCodes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		args []string
		want int
	}{
		{[]string{"version"}, cli.ExitOK},
		{[]string{"nope"}, cli.ExitUsage},
		{[]string{"version", "--bogus"}, cli.ExitUsage},
		{[]string{"version", "--format", "md"}, cli.ExitUsage},
		{[]string{"config", "show", "--config", "/does/not/exist.yaml"}, cli.ExitUsage},
	}
	for _, tt := range tests {
		d, _, stderr := newDeps(t)
		assert.Equal(t, tt.want, cli.Execute(t.Context(), d, tt.args), "%v: %s", tt.args, stderr)
	}
}

func TestInterruptedExits130(t *testing.T) {
	t.Parallel()
	d, _, stderr := newDeps(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	assert.Equal(t, cli.ExitInterrupted, cli.Execute(ctx, d, []string{"version"}))
	assert.Contains(t, stderr.String(), "interrupted")
}

// Only the root may set PersistentPreRun(E): cobra runs just the nearest one, so a
// subcommand hook would skip logger setup and leave Deps.Logger nil.
func TestOnlyRootHasPersistentPreRun(t *testing.T) {
	t.Parallel()
	d, _, _ := newDeps(t)
	root := cli.NewRootCmd(d)
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			assert.Nil(t, sub.PersistentPreRunE, sub.CommandPath())
			assert.Nil(t, sub.PersistentPreRun, sub.CommandPath())
			walk(sub)
		}
	}
	walk(root)
}

func TestUsageHintNamesFailingCommand(t *testing.T) {
	t.Parallel()
	d, _, stderr := newDeps(t)
	assert.Equal(t, cli.ExitUsage, cli.Execute(t.Context(), d, []string{"config", "show", "--bogus"}))
	assert.Contains(t, stderr.String(), "Run 'tfcensus config show --help' for usage.")
}
