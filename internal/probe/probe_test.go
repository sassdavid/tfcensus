package probe_test

import (
	"errors"
	"net/http"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
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
		"eu-west-1: access denied for resource-explorer-2:ListViews. Grant the actions in the read-only policy: https://tfcensus.sasskovacs.dev/iam/",
		"eu-west-1: AWS Config recorder default is stopped, so the config layer is off here.",
		"us-east-1: access denied for config:DescribeConfigurationRecorders. Grant the actions in the read-only policy: https://tfcensus.sasskovacs.dev/iam/",
		"us-east-1: access denied for config:DescribeConfigurationAggregators. Grant the actions in the read-only policy: https://tfcensus.sasskovacs.dev/iam/",
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
	assert.Contains(
		t,
		r.Notes,
		"access denied for resource-explorer-2:ListIndexes. Grant the actions in the read-only policy: https://tfcensus.sasskovacs.dev/iam/",
	)
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

// TestProbeAggregatorPages reads every page of DescribeConfigurationAggregators.
func TestProbeAggregatorPages(t *testing.T) {
	t.Parallel()
	page := func(name, next string) awstest.Stub {
		body := `{"ConfigurationAggregators":[{"ConfigurationAggregatorName":"` + name + `"}]`
		if next != "" {
			body += `,"NextToken":"` + next + `"`
		}
		return awstest.Stub{Body: body + "}"}
	}
	rt := awstest.NewRouter(t).
		On("resource-explorer-2", "ListIndexes", "", awstest.Stub{Body: `{"Indexes":[]}`}).
		On("config", "DescribeConfigurationRecorders", "", awstest.Stub{Body: `{"ConfigurationRecorders":[]}`}).
		On("config", "DescribeConfigurationAggregators", `"NextToken":"page-2"`, page("acme-a", "")).
		On("config", "DescribeConfigurationAggregators", "", page("acme-b", "page-2"))
	acct := &accounts.Account{ID: "111111111111", Partition: "aws", Config: aws.Config{
		Region:           "eu-west-1",
		Credentials:      credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:       &http.Client{Transport: rt},
		RetryMaxAttempts: 1,
	}}
	r, err := probe.New(probe.Options{}).Probe(t.Context(), acct, []string{"eu-west-1"})
	require.NoError(t, err)
	require.Len(t, r.Regions, 1)
	assert.Equal(t, probe.StatusAvailable, r.Regions[0].Aggregators.Status)
	assert.Equal(t, []string{"acme-a", "acme-b"}, r.Regions[0].Aggregators.Names)
}

func routerAccount(rt http.RoundTripper) *accounts.Account {
	return &accounts.Account{ID: "333333333333", Partition: "aws", Config: aws.Config{
		Region:           "eu-central-1",
		Credentials:      credentials.NewStaticCredentialsProvider("AKIAFAKE", "fake-secret", ""),
		HTTPClient:       &http.Client{Transport: rt},
		RetryMaxAttempts: 1,
	}}
}

// TestProbeAggregator reads the named aggregator and every page of its sources status, keeps the
// result in the cache, and reports a missing aggregator and denied calls.
func TestProbeAggregator(t *testing.T) {
	t.Parallel()
	agg := awstest.Stub{Body: `{"ConfigurationAggregators":[{"ConfigurationAggregatorName":"acme-org"}]}`}
	source := func(id, typ, region, status string) string {
		return `{"SourceId":"` + id + `","SourceType":"` + typ + `","AwsRegion":"` + region +
			`","LastUpdateStatus":"` + status + `"}`
	}

	t.Run("available", func(t *testing.T) {
		t.Parallel()
		rt := awstest.NewRouter(t).
			On("config", "DescribeConfigurationAggregators", `"acme-org"`, agg).
			On("config", "DescribeConfigurationAggregatorSourcesStatus", `"NextToken":"page-2"`, awstest.Stub{
				Body: `{"AggregatedSourceStatusList":[` + source(
					"222222222222",
					"ACCOUNT",
					"eu-west-1",
					"OUTDATED",
				) + `]}`,
			}).
			On("config", "DescribeConfigurationAggregatorSourcesStatus", "", awstest.Stub{
				Body: `{"AggregatedSourceStatusList":[` + source(
					"o-acme",
					"ORGANIZATION",
					"eu-west-1",
					"SUCCEEDED",
				) + `,` +
					source(
						"o-acme",
						"ORGANIZATION",
						"us-east-1",
						"FAILED",
					) + `],"NextToken":"page-2"}`,
			})
		dir := t.TempDir()
		now := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
		clock := func() time.Time { return now }
		rep := probe.New(probe.Options{CacheDir: dir, Clock: clock}).
			Aggregator(t.Context(), routerAccount(rt), "", "acme-org")
		assert.Equal(t, &probe.AggregatorReport{
			Name: "acme-org", Account: "333333333333", Region: "eu-central-1", Status: probe.StatusAvailable,
			Sources: []probe.AggSource{
				{Source: "222222222222", Type: probe.SourceAccount, Region: "eu-west-1", Status: "outdated"},
				{Source: "o-acme", Type: probe.SourceOrganization, Region: "eu-west-1", Status: probe.SyncSucceeded},
				{Source: "o-acme", Type: probe.SourceOrganization, Region: "us-east-1", Status: "failed"},
			},
			ProbedAt: now,
		}, rep)

		ct := &countingTransport{}
		now = now.Add(23 * time.Hour)
		cached := probe.New(probe.Options{CacheDir: dir, Clock: clock}).
			Aggregator(t.Context(), routerAccount(ct), "eu-central-1", "acme-org")
		assert.Zero(t, ct.calls.Load(), "a fresh cache needs no calls")
		assert.True(t, cached.Cached)
		assert.Equal(t, rep.Sources, cached.Sources)
	})

	t.Run("missing", func(t *testing.T) {
		t.Parallel()
		rt := awstest.NewRouter(t).On("config", "DescribeConfigurationAggregators", "", awstest.Stub{
			Status: http.StatusBadRequest,
			Body:   awstest.JSONError("NoSuchConfigurationAggregatorException", "no aggregator"),
		})
		rep := probe.New(probe.Options{}).Aggregator(t.Context(), routerAccount(rt), "eu-central-1", "acme-org")
		assert.Equal(t, probe.StatusUnavailable, rep.Status)
		assert.Empty(t, rep.Error)
	})

	t.Run("sources denied", func(t *testing.T) {
		t.Parallel()
		rt := awstest.NewRouter(t).
			On("config", "DescribeConfigurationAggregators", "", agg).
			On("config", "DescribeConfigurationAggregatorSourcesStatus", "", awstest.Stub{
				Status: http.StatusBadRequest, Body: awstest.JSONError("AccessDeniedException", "denied"),
			})
		rep := probe.New(probe.Options{}).Aggregator(t.Context(), routerAccount(rt), "eu-central-1", "acme-org")
		assert.Equal(t, probe.StatusDenied, rep.Status)
		assert.Equal(t, "access denied for config:DescribeConfigurationAggregatorSourcesStatus", rep.Error)
		assert.Empty(t, rep.Sources)
	})
}

// TestAggregatorServes: the aggregator serves a pair when a source covers it and every source
// that covers it succeeded.
func TestAggregatorServes(t *testing.T) {
	t.Parallel()
	rep := &probe.AggregatorReport{Status: probe.StatusAvailable, Sources: []probe.AggSource{
		{Source: "o-acme", Type: probe.SourceOrganization, Region: "eu-west-1", Status: probe.SyncSucceeded},
		{Source: "o-acme", Type: probe.SourceOrganization, Region: "us-east-1", Status: "outdated"},
		{Source: "222222222222", Type: probe.SourceAccount, Region: "eu-central-1", Status: probe.SyncSucceeded},
		{Source: "333333333333", Type: probe.SourceAccount, Region: "eu-west-1", Status: "failed"},
	}}
	for _, tc := range []struct {
		name            string
		rep             *probe.AggregatorReport
		account, region string
		want            bool
	}{
		{"organization source", rep, "111111111111", "eu-west-1", true},
		{"outdated source", rep, "111111111111", "us-east-1", false},
		{"account source", rep, "222222222222", "eu-central-1", true},
		{"other account", rep, "111111111111", "eu-central-1", false},
		{"one failed source", rep, "333333333333", "eu-west-1", false},
		{"no source in region", rep, "111111111111", "eu-north-1", false},
		{"unavailable", &probe.AggregatorReport{Status: probe.StatusDenied, Sources: rep.Sources}, "111111111111", "eu-west-1", false},
		{"nil", nil, "111111111111", "eu-west-1", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, tc.rep.Serves(tc.account, tc.region))
		})
	}
}
