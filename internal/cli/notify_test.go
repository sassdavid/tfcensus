package cli_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
)

// runNotify runs tfcensus notify with env added and requests going to the network, which in
// these tests is only a local httptest server.
func runNotify(t *testing.T, cfg string, env []string, args ...string) cliResult {
	t.Helper()
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
		Env: append(app.Env{
			"PATH=" + filepath.Join(
				home,
				"no-bin",
			),
			"HOME=" + home,
			"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
			"NO_COLOR=1",
		}, env...),
		Clock: func() time.Time { return time.Date(2026, 11, 12, 9, 0, 0, 0, time.UTC) },
		Getwd: func() (string, error) { return home, nil },
	}
	code := cli.Execute(t.Context(), d, append([]string{"notify", "--config", cfg}, args...))
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

func notifyConfig(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "tfcensus.yaml")
	require.NoError(t, os.WriteFile(p, []byte("schema_version: 1\n"+body), 0o600))
	return p
}

func TestNotifyFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "notify out")
	cfg := notifyConfig(t, "notify:\n  enabled: [file]\n  file:\n    dir: "+filepath.ToSlash(dir)+"\n")
	from := []string{"--from", fixture("drift-values.json"), "--from", fixture("scan.json"), "--from", auditJSON}

	res := runNotify(t, cfg, nil, from...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, 6, strings.Count(res.stdout, "  write "), res.stdout)
	for _, name := range []string{"tfcensus-drift.md", "tfcensus-drift.json", "tfcensus-scan.md", "tfcensus-audit.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)
		for _, v := range driftValues() {
			assert.NotContains(t, string(b), v, "%s holds a drift attribute value", name)
		}
	}
	md, err := os.ReadFile(filepath.Join(dir, "tfcensus-drift.md"))
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join(goldenReport, "notify-drift.md"), md)

	res = runNotify(t, cfg, nil, append(from, "--format", "json")...)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.Equal(t, 6, strings.Count(res.stdout, `"op": "unchanged"`), "a re-run changes nothing")
}

func TestNotifyDryRunGitHub(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var methods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method+" "+r.URL.Path)
		mu.Unlock()
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, "[]")
	}))
	t.Cleanup(srv.Close)
	cfg := notifyConfig(t, "")
	env := []string{"GITHUB_REPOSITORY=acme/infra-live", "GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=test-token"}

	res := runNotify(
		t,
		cfg,
		env,
		"--from",
		fixture("drift.json"),
		"--notify",
		"github-issues",
		"--dry-run-notify",
		"-vvv",
	)
	require.Equal(t, cli.ExitOK, res.code, res.stderr)
	testutil.Golden(t, filepath.Join(goldenReport, "notify-dry-run.txt"), []byte(res.stdout))
	assert.Equal(t, []string{"GET /repos/acme/infra-live/issues"}, methods, "a dry run only reads")
	assert.NotContains(t, res.stderr, "test-token", "-vvv redacts the token")
	assert.Contains(t, res.stderr, "[redacted]")
}

func TestNotifyErrors(t *testing.T) {
	t.Parallel()
	cfg := notifyConfig(t, "")
	res := runNotify(t, cfg, nil, "--from", fixture("drift.json"))
	assert.Equal(t, cli.ExitUsage, res.code, "no notifier enabled")

	res = runNotify(t, cfg, nil, "--from", fixture("drift.json"), "--notify", "pager")
	assert.Equal(t, cli.ExitUsage, res.code)

	res = runNotify(t, cfg, nil, "--from", indexJSON, "--notify", "file")
	assert.Equal(t, cli.ExitUsage, res.code, "an index isn't a report")

	res = runNotify(t, cfg, nil, "--from", fixture("drift.json"), "--notify", "github-issues")
	assert.Equal(t, cli.ExitUsage, res.code, "no repository or token")
	assert.Contains(t, res.stdout+res.stderr, "GITHUB_REPOSITORY")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"message":"Bad credentials"}`, http.StatusUnauthorized)
	}))
	t.Cleanup(srv.Close)
	env := []string{"GITHUB_REPOSITORY=acme/infra-live", "GITHUB_API_URL=" + srv.URL, "GITHUB_TOKEN=bad"}
	res = runNotify(t, cfg, env, "--from", fixture("drift.json"), "--notify", "github-issues")
	assert.Equal(t, cli.ExitAuth, res.code, res.stderr)
}
