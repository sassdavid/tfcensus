package cli_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/cli"
	"github.com/sassdavid/tfcensus/internal/testutil"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const (
	scanAggCassette = cassetteDir + "/aws/scan-aggregator"
	auditAcct       = "444455556666"
)

// aggregatorConfig writes the scan fixture config with accounts.aggregator naming the acme-org
// aggregator in the acme-audit profile's account.
func aggregatorConfig(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(scanConfig)
	require.NoError(t, err)
	cfg := string(b) + "accounts:\n  aggregator: { profile: acme-audit, name: acme-org, region: eu-central-1 }\n"
	path := filepath.Join(t.TempDir(), "tfcensus.yaml")
	require.NoError(t, os.WriteFile(path, []byte(cfg), 0o600))
	return path
}

// aggregatorTransport replays the scan-aggregator cassette, or writes it when goldens are updated.
func aggregatorTransport(t *testing.T) http.RoundTripper {
	t.Helper()
	if !testutil.Updating() {
		return awstest.Replay(t, scanAggCassette)
	}
	fake := awstest.NewFake(t, time.Date(2026, 11, 9, 8, 30, 0, 0, time.UTC))
	fake.LoadManifest(scanManifest)
	fake.Keys = map[string]string{"AKIAFAKEDEFAULT": scanAcct, "AKIAFAKEAUDIT": auditAcct}
	router := awstest.NewRouter(t)
	scanStubs(router, false, true)
	return awstest.WriteCassette(t, scanAggCassette, splitTransport{fake: fake, router: router})
}

type scanJSON struct {
	Data struct {
		Findings json.RawMessage `json:"findings"`
		Runs     []struct {
			Layer  string `json:"layer"`
			Via    string `json:"via"`
			Status string `json:"status"`
		} `json:"runs"`
		Aggregator *struct {
			Name     string `json:"name"`
			Account  string `json:"account"`
			Status   string `json:"status"`
			Served   int    `json:"served"`
			Fallback int    `json:"fallback"`
		} `json:"aggregator"`
	} `json:"data"`
}

func scanFindings(t *testing.T, rt http.RoundTripper, home string, args ...string) scanJSON {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := cli.Execute(t.Context(), scanDeps(t, rt, home, &stdout, &stderr),
		append([]string{"scan", "tf-scan", "--format", "json", "--concurrency", "1"}, args...))
	require.Equal(t, cli.ExitOK, code, stderr.String())
	var out scanJSON
	require.NoError(t, json.Unmarshal(stdout.Bytes(), &out))
	return out
}

// TestScanAggregator: with accounts.aggregator set, scan reads the config layer through the
// aggregator in the audit account and makes no SelectResourceConfig call (the cassette has none).
// The findings match the scan that queries the account itself, and doctor reports the aggregator
// it found.
func TestScanAggregator(t *testing.T) {
	t.Parallel()
	if testutil.Updating() {
		t.Skip("TestScanCassette writes scan-fleet, which this test compares against")
	}
	cfg := aggregatorConfig(t)

	agg := scanFindings(t, aggregatorTransport(t), fleetHome(t), "--config", cfg)
	require.NotNil(t, agg.Data.Aggregator, "the report has the aggregator outcome")
	assert.Equal(t, "acme-org", agg.Data.Aggregator.Name)
	assert.Equal(t, auditAcct, agg.Data.Aggregator.Account)
	assert.Equal(t, "ok", agg.Data.Aggregator.Status)
	assert.Equal(t, 1, agg.Data.Aggregator.Served)
	assert.Zero(t, agg.Data.Aggregator.Fallback)
	for _, r := range agg.Data.Runs {
		if r.Layer == "config" {
			assert.Equal(t, "aggregator", r.Via)
		}
	}

	direct := scanFindings(t, awstest.Replay(t, scanCassette), fleetHome(t), "--config", scanConfig)
	assert.Nil(t, direct.Data.Aggregator)
	assert.JSONEq(t, string(direct.Data.Findings), string(agg.Data.Findings),
		"the aggregator and the account's own recorder give the same findings")

	var stdout, stderr bytes.Buffer
	code := cli.Execute(t.Context(), scanDeps(t, awstest.Replay(t, scanAggCassette), fleetHome(t), &stdout, &stderr),
		[]string{"doctor", "--config", cfg, "--format", "plain"})
	require.Equal(t, cli.ExitOK, code, stderr.String())
	assert.Contains(t, stdout.String(), "AWS Config aggregator acme-org (acme-audit, eu-central-1): found, "+
		"1 source in 1 region. scan and whois read AWS Config through it where its sources are current\n")
	assert.NotContains(t, stderr.String(), "aggregator")
}

// TestScanAggregatorCassette writes the scan-aggregator cassette when goldens are updated.
func TestScanAggregatorCassette(t *testing.T) { //nolint:paralleltest // writes the cassette TestScanAggregator reads
	if !testutil.Updating() {
		t.Skip("replayed by TestScanAggregator; run mise run test:golden -- -update to rewrite it")
	}
	scanFindings(t, aggregatorTransport(t), fleetHome(t), "--config", aggregatorConfig(t))
}
