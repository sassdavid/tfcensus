package cli_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/mcpkit"
	"github.com/sassdavid/tfcensus/internal/mcpserver"
	"github.com/sassdavid/tfcensus/internal/testutil/mcptest"
)

// connectMCP runs tfcensus mcp args in-process, with no terragrunt on PATH and a transport that
// fails the test on any request, and connects a client to it.
func connectMCP(t *testing.T, args ...string) *mcptest.Client {
	t.Helper()
	home := t.TempDir()
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &bytes.Buffer{},
		Stderr: &bytes.Buffer{},
		Env: app.Env{
			"PATH=" + filepath.Join(
				home,
				"no-bin",
			),
			"HOME=" + home,
			"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
			"NO_COLOR=1",
		},
		Clock:         func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
		Getwd:         func() (string, error) { return home, nil },
		HTTPTransport: failTransport{t},
	}
	ready := make(chan *mcptest.Client, 1)
	done := make(chan error, 1)
	go func() {
		done <- cli.ServeMCP(t.Context(), d, args, func(_ context.Context, s *mcpkit.Server) error {
			ready <- mcptest.Connect(t, s)
			return nil
		})
	}()
	select {
	case c := <-ready:
		require.NoError(t, <-done)
		return c
	case err := <-done:
		t.Fatalf("tfcensus mcp: %v", err)
	}
	return nil
}

func allFiles() []string {
	return []string{
		"--index", indexJSON, "--scan", fixture("scan.json"), "--audit", auditJSON,
		"--drift", fixture("drift-values.json"), "--max-result-bytes", "0",
	}
}

// TestSpec_notifications_and_reports_3 calls every MCP tool in-process and checks that each
// answers like its CLI twin (the report goldens), that every tool is marked read-only and
// closed-world, and that no tool can move state. connectMCP fails the test on any network
// request, and no program is on PATH.
func TestSpec_notifications_and_reports_3(t *testing.T) {
	t.Parallel()
	c := connectMCP(t, allFiles()...)
	assert.Equal(t, mcpserver.Name, c.ServerInfo().Name)

	tools := c.Tools(t)
	var names []string
	for _, tool := range tools {
		names = append(names, tool.Name)
		a := tool.Annotations
		require.NotNil(t, a, tool.Name)
		assert.True(t, a.ReadOnlyHint, "%s is read-only", tool.Name)
		assert.False(t, *a.DestructiveHint, "%s is not destructive", tool.Name)
		assert.False(t, *a.OpenWorldHint, "%s does not reach the network", tool.Name)
		for _, banned := range []string{"move", "apply", "rollback", "mark", "plan", "exec", "write", "delete"} {
			assert.NotContains(t, tool.Name, banned)
		}
	}
	slices.Sort(names)
	want := mcpserver.Tools()
	slices.Sort(want)
	assert.Equal(t, want, names)

	for _, tc := range reportCases() {
		mcpGolden(t, c, tc.tool, tc.mcpArgs, filepath.Join(goldenReport, tc.golden))
	}
	whois := map[string]any{"queries": []string{"arn:aws:iam::111111111111:role/app", "sg-0123456789abcdef0"}}
	text := string(mcptest.Text(t, c.Call(t, "whois", whois)))
	assert.Contains(t, text, `"answers"`)
	for _, v := range driftValues() {
		assert.NotContains(t, text, v)
	}
	drift := string(mcptest.Text(t, c.Call(t, "drift_summary", map[string]any{})))
	for _, v := range driftValues() {
		assert.NotContains(t, drift, v, "drift_summary shows a drift attribute value")
	}
}

// mcpGolden is mcptest.Golden with the version made stable, as the CLI goldens have it.
func mcpGolden(t *testing.T, c *mcptest.Client, tool string, args any, path string) {
	t.Helper()
	text := mcptest.Text(t, c.Call(t, tool, args))
	require.Nil(t, mcptest.PageOf(t, text), "%s split its result", tool)
	want, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, string(want), string(stableJSON(string(mcptest.Indent(t, text)))), "%s differs from %s", tool, path)
}

// TestMCPWhoisMatchesCLI compares the whois tool with tfcensus whois --index --format json.
func TestMCPWhoisMatchesCLI(t *testing.T) {
	t.Parallel()
	queries := []string{"arn:aws:iam::111111111111:role/app", "sg-0123456789abcdef0", "arn:aws:s3:::acme-assets"}
	res := runOffline(t, append([]string{"whois", "--index", indexJSON, "--format", "json"}, queries...)...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	c := connectMCP(t, "--index", indexJSON)
	got := mcptest.Indent(t, mcptest.Text(t, c.Call(t, "whois", map[string]any{"queries": queries})))
	assert.Equal(t, res.stdout, string(got))
}

func TestMCPPagesAndErrors(t *testing.T) {
	t.Parallel()
	c := connectMCP(t, "--index", indexJSON, "--scan", fixture("scan.json"), "--max-result-bytes", "2500")
	pages := c.Pages(t, "scan_summary", map[string]any{"category": "managed"})
	require.Greater(t, len(pages), 1, "a small cap splits the result")
	total := 0
	for _, p := range pages {
		page := mcptest.PageOf(t, p)
		require.NotNil(t, page)
		total += page.Returned
		assert.LessOrEqual(t, len(p), 2500)
	}
	assert.Equal(t, mcptest.PageOf(t, pages[0]).Total, total)

	msg := mcptest.ErrorText(t, c.Call(t, "audit_findings", map[string]any{}))
	assert.Contains(t, msg, "--audit", "a missing file names its flag")
	msg = mcptest.ErrorText(t, c.Call(t, "whois", map[string]any{"queries": []string{}}))
	assert.Contains(t, msg, "queries")
	msg = mcptest.ErrorText(t, c.Call(t, "scan_summary", map[string]any{"top": 9999}))
	assert.Contains(t, msg, "top")
}
