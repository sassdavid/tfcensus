package cli_test

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/txtar"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/fakebin"
	"github.com/sassdavid/tfcensus/internal/testutil/mcptest"
)

const (
	reportsDir   = "../../testdata/reports"
	goldenReport = "../../testdata/golden/report"
	auditJSON    = "../../testdata/golden/audit/tf-audit.json"
	indexJSON    = "../../testdata/whois/index.json"
)

// driftValues are attribute values in the drift fixture plans; drift-values.json holds them because
// it was written with --show-attributes.
func driftValues() []string {
	return []string{"10.42.0.0/16", "db.t4g", "finance", "c2hhMjU2", "team-a", "FIXTURE-SECRET"}
}

func fixture(name string) string { return filepath.Join(reportsDir, name) }

// runOffline runs tfcensus with no terragrunt on PATH and no network: every request fails the test.
func runOffline(t *testing.T, args ...string) cliResult {
	t.Helper()
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	d := &app.Deps{
		Stdin:  strings.NewReader(""),
		Stdout: &stdout,
		Stderr: &stderr,
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
	code := cli.Execute(t.Context(), d, args)
	return cliResult{stdout: stdout.String(), stderr: stderr.String(), code: code}
}

type failTransport struct{ t *testing.T }

func (f failTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.t.Errorf("unexpected network request: %s %s", r.Method, r.URL)
	return nil, io.ErrUnexpectedEOF
}

func archiveFile(t *testing.T, archive, name string) []byte {
	t.Helper()
	a, err := txtar.ParseFile(archive)
	require.NoError(t, err)
	for _, f := range a.Files {
		if f.Name == name {
			return f.Data
		}
	}
	t.Fatalf("%s has no %s", archive, name)
	return nil
}

// TestReportFixtures keeps testdata/reports in step with the outputs it copies: the scan and drift
// JSON goldens of the spec scripts, and a drift run with --show-attributes.
func TestReportFixtures(t *testing.T) {
	t.Parallel()
	testutil.Golden(
		t,
		fixture("scan.json"),
		archiveFile(t, "../../testdata/script/spec_scan_1.txtar", "golden/scan.json"),
	)
	testutil.Golden(
		t,
		fixture("drift.json"),
		archiveFile(t, "../../testdata/script/spec_drift_1.txtar", "golden/drift.json"),
	)

	bin := fakebin.Install(t, "terragrunt", fakebin.Spec{})
	res := runCLI(
		t,
		bin,
		"drift",
		"--config",
		driftConfig(t),
		"--from-json-out-dir",
		absPath(t, filepath.Join(driftIngestDir, "plans")),
		"--report-file",
		absPath(t, filepath.Join(driftIngestDir, "report.json")),
		"--show-attributes",
		"--format",
		"json",
	)
	require.Equal(t, cli.ExitError, res.code, res.stderr)
	require.Contains(t, res.stdout, "10.42.0.0/16")
	testutil.Golden(t, fixture("drift-values.json"), stableJSON(res.stdout))
}

func htmlArgs(out string) []string {
	return []string{
		"report", "html", "--from", fixture("scan.json"), "--from", fixture("scan-earlier.json"),
		"--from", fixture("drift-values.json"), "--from", auditJSON, "-o", out,
	}
}

// TestSpec_notifications_and_reports_2: the HTML report is one self-contained file, byte-identical
// across runs, that loads nothing from the network and renders in a headless browser.
func TestSpec_notifications_and_reports_2(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "out dir")
	require.NoError(t, os.MkdirAll(dir, 0o750))
	first, second := filepath.Join(dir, "census.html"), filepath.Join(dir, "again.html")
	for _, out := range []string{first, second} {
		res := runOffline(t, htmlArgs(out)...)
		require.Equal(t, cli.ExitOK, res.code, res.stderr)
		assert.Empty(t, res.stdout)
	}
	a, err := os.ReadFile(first)
	require.NoError(t, err)
	b, err := os.ReadFile(second)
	require.NoError(t, err)
	assert.Equal(t, a, b, "two runs give the same bytes")
	page := strings.ReplaceAll(string(a), "tfcensus dev", "tfcensus test")
	testutil.Golden(t, filepath.Join(goldenReport, "census.html"), []byte(page))

	assert.Contains(t, page, `default-src 'none'`)
	assert.Regexp(t, `<polyline class="series"`, page, "two scans draw a trend")
	external := regexp.MustCompile(`(?i)(src|href|action)\s*=\s*"(https?:)?//`)
	assert.Empty(t, external.FindAllString(page, -1), "no external resources")
	assert.NotContains(t, page, "@import")
	for _, v := range driftValues() {
		assert.NotContains(t, page, v, "the report shows a drift attribute value")
	}

	browserSmoke(t, first)
}

// browserSmoke opens the report in headless Chrome with every host unresolvable and checks that
// the page script ran. CI runners have Chrome; elsewhere the check is skipped.
func browserSmoke(t *testing.T, page string) {
	t.Helper()
	chrome := ""
	for _, name := range []string{"google-chrome", "google-chrome-stable", "chromium", "chromium-browser", "chrome"} {
		if p, err := exec.LookPath(name); err == nil {
			chrome = p
			break
		}
	}
	if mac := "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"; chrome == "" && runtime.GOOS == "darwin" {
		if _, err := os.Stat(mac); err == nil {
			chrome = mac
		}
	}
	if chrome == "" {
		if os.Getenv("CI") != "" && runtime.GOOS == "linux" {
			t.Fatal("headless smoke test: no Chrome on the CI runner")
		}
		t.Log("headless smoke test skipped: no Chrome found")
		return
	}
	abs, err := filepath.Abs(page)
	require.NoError(t, err)
	out, err := exec.CommandContext(t.Context(), chrome, "--headless=new", "--disable-gpu", "--no-sandbox",
		"--no-first-run", "--user-data-dir="+t.TempDir(), `--host-resolver-rules=MAP * ~NOTFOUND`,
		"--virtual-time-budget=5000", "--dump-dom", mcptest.FileURI(abs)).
		Output()
	require.NoError(t, err)
	assert.Contains(t, string(out), `data-ready="ok"`, "the page script ran")
	assert.Regexp(t, `\d+ of \d+ shown`, string(out), "the filters counted the rows")
}

func TestReportHTMLErrors(t *testing.T) {
	t.Parallel()
	res := runOffline(t, "report", "html", "--from", indexJSON)
	assert.Equal(t, cli.ExitUsage, res.code)
	assert.Contains(t, res.stderr, "state index")

	res = runOffline(t, "report", "html")
	assert.Equal(t, cli.ExitUsage, res.code)

	res = runOffline(t, "report", "html", "--from", fixture("scan.json"))
	assert.Equal(t, cli.ExitOK, res.code, res.stderr)
	assert.True(t, strings.HasPrefix(res.stdout, "<!doctype html>"), "-o defaults to stdout")
}

// reportCases are the report commands whose JSON is also what the MCP tools return.
func reportCases() []struct {
	golden, tool string
	args         []string
	mcpArgs      map[string]any
} {
	return []struct {
		golden, tool string
		args         []string
		mcpArgs      map[string]any
	}{
		{"units.json", "units", []string{"report", "units", "--index", indexJSON}, map[string]any{}},
		{
			"units-iam.json",
			"units",
			[]string{"report", "units", "--index", indexJSON, "--path", "*/iam"},
			map[string]any{"path": "*/iam"},
		},
		{"inventory.json", "inventory", []string{"report", "inventory", "--index", indexJSON}, map[string]any{}},
		{
			"inventory-iam.json",
			"inventory",
			[]string{"report", "inventory", "--index", indexJSON, "--type", "aws_iam_*"},
			map[string]any{"type": "aws_iam_*"},
		},
		{"scan.json", "scan_summary", []string{"report", "scan", "--from", fixture("scan.json")}, map[string]any{}},
		{
			"scan-account.json",
			"scan_summary",
			[]string{
				"report",
				"scan",
				"--from",
				fixture("scan.json"),
				"--account",
				"111111111111",
				"--category",
				"managed",
				"--top",
				"2",
			},
			map[string]any{"account": "111111111111", "category": "managed", "top": 2},
		},
		{"audit.json", "audit_findings", []string{"report", "audit", "--from", auditJSON}, map[string]any{}},
		{
			"audit-high.json", "audit_findings",
			[]string{"report", "audit", "--from", auditJSON, "--severity", "high", "--include-suppressed"},
			map[string]any{"severity": "high", "include_suppressed": true},
		},
		{
			"drift.json",
			"drift_summary",
			[]string{"report", "drift", "--from", fixture("drift-values.json")},
			map[string]any{},
		},
		{
			"drift-clean.json",
			"drift_summary",
			[]string{"report", "drift", "--from", fixture("drift.json"), "--status", "clean"},
			map[string]any{"status": "clean"},
		},
	}
}

func TestReportGoldens(t *testing.T) {
	t.Parallel()
	for _, tc := range reportCases() {
		t.Run(tc.golden, func(t *testing.T) {
			t.Parallel()
			res := runOffline(t, append(tc.args, "--format", "json")...)
			require.Equal(t, cli.ExitOK, res.code, res.stderr)
			testutil.Golden(t, filepath.Join(goldenReport, tc.golden), stableJSON(res.stdout))
			for _, v := range driftValues() {
				assert.NotContains(t, res.stdout, v)
			}
			text := runOffline(t, append(tc.args, "--format", "plain")...)
			require.Equal(t, cli.ExitOK, text.code, text.stderr)
			assert.NotEmpty(t, text.stdout)
		})
	}
}

func TestReportErrors(t *testing.T) {
	t.Parallel()
	for _, args := range [][]string{
		{"report", "units"},
		{"report", "audit"},
		{"report", "units", "--index", fixture("scan.json")},
		{"report", "scan", "--from", fixture("scan.json"), "--top", "501"},
		{"report", "scan", "--from", fixture("scan.json"), "--category", "nope"},
		{"report", "audit", "--from", auditJSON, "--severity", "urgent"},
		{"report", "drift", "--from", fixture("drift.json"), "--status", "nope"},
		{"report", "scan"}, // an empty scan cache
	} {
		res := runOffline(t, args...)
		assert.Equal(t, cli.ExitUsage, res.code, "%v: %s", args, res.stderr)
	}
}
