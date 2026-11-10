package probe_test

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/accounts"
	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/probe"
	"github.com/sassdavid/tfcensus/internal/testutil/awstest"
)

const cassettes = "../../testdata/cassettes/aws"

func account(t *testing.T, cassette string) *accounts.Account {
	t.Helper()
	home := t.TempDir()
	awstest.WriteConfig(t, home, []awstest.Profile{{Name: "acme-edge", Region: "eu-west-1"}}, "")
	l := accounts.New(accounts.Options{
		Lookup:      app.Env{"HOME=" + home}.Lookup,
		Transport:   awstest.Replay(t, filepath.Join(cassettes, cassette)),
		MaxAttempts: 1,
	})
	a, err := l.Profile(t.Context(), "acme-edge")
	require.NoError(t, err)
	return a
}

// TestProbeEdgeCases covers an aggregator index, denied APIs, a stopped DAILY recorder, views
// without a default view, and a region with nothing set up.
func TestProbeEdgeCases(t *testing.T) {
	t.Parallel()
	r, err := probe.New(probe.Options{}).Probe(t.Context(), account(t, "probe-edge"),
		[]string{"us-east-1", "eu-west-1", "eu-north-1", "eu-west-1"})
	require.NoError(t, err)

	assert.Equal(t, probe.StatusAvailable, r.ResourceExplorer.Status)
	assert.Equal(t, 3, r.ResourceExplorer.SupportedTypes, "asked in the aggregator region")
	require.Len(t, r.Regions, 3, "regions are sorted and deduplicated")
	north, west, east := r.Regions[0], r.Regions[1], r.Regions[2]

	assert.Equal(t, "eu-north-1", north.Region)
	assert.Equal(t, []probe.Layer{probe.LayerCC, probe.LayerNative}, north.Layers)

	assert.Equal(t, probe.StatusDenied, west.ResourceExplorer.Status)
	assert.Equal(t, probe.StatusUnavailable, west.Config.Status)
	assert.Equal(t, []probe.Recorder{{Name: "default", IncludeGlobal: true, Frequency: "DAILY"}}, west.Config.Recorders)
	assert.Equal(t, []string{"acme-org"}, west.Aggregators.Names)
	assert.Equal(t, []probe.Layer{probe.LayerCC, probe.LayerNative}, west.Layers)

	assert.Equal(
		t,
		probe.RegionRE{Status: probe.StatusAvailable, Index: probe.IndexAggregator, Views: 2},
		east.ResourceExplorer,
	)
	assert.Equal(t, probe.StatusDenied, east.Config.Status)
	assert.Equal(t, probe.StatusDenied, east.Aggregators.Status)
	assert.Equal(t, []probe.Layer{probe.LayerRE, probe.LayerCC, probe.LayerNative}, east.Layers)

	assert.Equal(t, []string{
		"Resource Explorer supports 3 resource types.",
		"eu-north-1: no Resource Explorer index. An admin can create one (`aws resource-explorer-2 create-index --region eu-north-1`).",
		"eu-north-1: no AWS Config recorder, so the config layer is off here.",
		"eu-west-1: access denied for resource-explorer-2:ListViews. Grant the actions in docs/iam/tfcensus-readonly.json.",
		"eu-west-1: AWS Config recorder default is stopped, so the config layer is off here.",
		"us-east-1: access denied for config:DescribeConfigurationRecorders. Grant the actions in docs/iam/tfcensus-readonly.json.",
		"us-east-1: access denied for config:DescribeConfigurationAggregators. Grant the actions in docs/iam/tfcensus-readonly.json.",
	}, r.Notes)
}

// TestProbeDenied records a denied ListIndexes and still probes the account's own region.
func TestProbeDenied(t *testing.T) {
	t.Parallel()
	r, err := probe.New(probe.Options{}).Probe(t.Context(), account(t, "probe-denied"), nil)
	require.NoError(t, err)
	assert.Equal(t, probe.StatusDenied, r.ResourceExplorer.Status)
	require.Len(t, r.Regions, 1)
	assert.Equal(t, "eu-west-1", r.Regions[0].Region)
	assert.Equal(t, []probe.Layer{probe.LayerCC, probe.LayerNative}, r.Regions[0].Layers)
	assert.Contains(t, r.Notes,
		"access denied for resource-explorer-2:ListIndexes. Grant the actions in docs/iam/tfcensus-readonly.json.")
}

type countingTransport struct{ calls atomic.Int32 }

func (c *countingTransport) RoundTrip(*http.Request) (*http.Response, error) {
	c.calls.Add(1)
	return nil, errors.New("no network in this test")
}

func TestProbeCache(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	acct := account(t, "probe-edge")
	regions := []string{"eu-north-1", "eu-west-1", "us-east-1"}

	first, err := probe.New(probe.Options{CacheDir: dir, Clock: clock}).Probe(t.Context(), acct, regions)
	require.NoError(t, err)
	assert.False(t, first.ResourceExplorer.Cached)

	offline := *acct
	offline.Config = acct.Config.Copy()
	ct := &countingTransport{}
	offline.Config.HTTPClient = &http.Client{Transport: ct}
	offline.Config.RetryMaxAttempts = 1

	now = now.Add(23 * time.Hour)
	cached, err := probe.New(probe.Options{CacheDir: dir, Clock: clock}).Probe(t.Context(), &offline, regions)
	require.NoError(t, err)
	assert.Zero(t, ct.calls.Load(), "a fresh cache needs no calls")
	assert.True(t, cached.ResourceExplorer.Cached)
	for _, reg := range cached.Regions {
		assert.True(t, reg.Cached, reg.Region)
	}
	assert.Equal(t, first.Notes, cached.Notes)

	refreshed, err := probe.New(probe.Options{CacheDir: dir, Clock: clock, Refresh: true}).
		Probe(t.Context(), &offline, regions)
	require.NoError(t, err)
	assert.NotZero(t, ct.calls.Load(), "--refresh probes again")
	assert.Equal(t, probe.StatusError, refreshed.ResourceExplorer.Status)

	now = now.Add(2 * time.Hour)
	ct.calls.Store(0)
	_, err = probe.New(probe.Options{CacheDir: dir, Clock: clock}).Probe(t.Context(), &offline, regions)
	require.NoError(t, err)
	assert.NotZero(t, ct.calls.Load(), "after 24 hours the cache is stale")
}
