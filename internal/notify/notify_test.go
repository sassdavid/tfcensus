package notify_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/notify"
	"github.com/sassdavid/tfcensus/internal/report"
	"github.com/sassdavid/tfcensus/internal/testutil"
)

func driftReport(scopes ...notify.Scope) *notify.Report {
	return &notify.Report{Kind: report.KindDrift, Scopes: scopes}
}

func unit(path string, items ...notify.Item) notify.Scope {
	return notify.Scope{Key: path, Title: "tfcensus: drift in " + path, Items: append([]notify.Item{}, items...)}
}

func change(addr string) notify.Item {
	return notify.Item{Level: "update", Type: "aws_vpc", Target: addr, Detail: "tags.Name"}
}

func newGitHub(t *testing.T, f *fakeGitHub, mod func(*notify.GitHubConfig)) *notify.GitHub {
	t.Helper()
	cfg := notify.GitHubConfig{
		Repo: "acme/infra-live", APIURL: f.srv.URL, Token: "gh-test-token", MaxOpen: 10, CloseResolved: true,
		Labels: []string{"infra"},
	}
	if mod != nil {
		mod(&cfg)
	}
	g, err := notify.NewGitHub(client(t), cfg)
	require.NoError(t, err)
	return g
}

func ops(res *notify.Result) []string {
	out := make([]string, 0, len(res.Actions))
	for _, a := range res.Actions {
		out = append(out, string(a.Op)+" "+a.Scope)
	}
	return out
}

// TestSpec_notifications_and_reports_1 runs the GitHub issues lifecycle against a fake API:
// create, a re-run that writes nothing, update and close, a dry run that only reads, max_open,
// duplicates, and a filtered run that leaves other scopes alone.
func TestSpec_notifications_and_reports_1(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFakeGitHub(t)
	f.prs = 1
	g := newGitHub(t, f, nil)

	first := driftReport(unit("tg-live/dev/vpc", change("aws_vpc.main")),
		unit("tg-live/prod/iam", change("aws_iam_role.app")), unit("tg-live/prod/vpc"))
	res, err := g.Publish(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, []string{"create tg-live/dev/vpc", "create tg-live/prod/iam"}, ops(res))
	assert.Equal(t, []string{
		"POST " + ghRepo + "/labels", "POST " + ghRepo + "/labels", "POST " + ghRepo + "/labels",
		"POST " + ghRepo + "/issues", "POST " + ghRepo + "/issues",
	}, f.writes())
	open := f.open()
	require.Len(t, open, 2)
	assert.ElementsMatch(
		t,
		[]string{"tfcensus", "tfcensus:drift", "infra"},
		open["tfcensus: drift in tg-live/dev/vpc"].Labels,
	)
	m, ok := notify.ParseMarker(open["tfcensus: drift in tg-live/dev/vpc"].Body)
	require.True(t, ok)
	assert.Equal(t, notify.Marker{Kind: report.KindDrift, Scope: "tg-live/dev/vpc", Hash: m.Hash}, m)

	res, err = g.Publish(ctx, first)
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged tg-live/dev/vpc", "unchanged tg-live/prod/iam"}, ops(res))
	assert.Empty(t, f.writes(), "a re-run with the same report writes nothing")

	second := driftReport(unit("tg-live/dev/vpc", change("aws_vpc.main"), change("aws_subnet.a")),
		unit("tg-live/prod/iam"), unit("tg-live/prod/vpc"))
	dry := newGitHub(t, f, func(c *notify.GitHubConfig) { c.DryRun = true })
	res, err = dry.Publish(ctx, second)
	require.NoError(t, err)
	assert.True(t, res.DryRun)
	assert.Equal(t, []string{"update tg-live/dev/vpc", "close tg-live/prod/iam"}, ops(res))
	assert.Empty(t, f.writes(), "--dry-run-notify only reads")

	res, err = g.Publish(ctx, second)
	require.NoError(t, err)
	assert.Equal(t, []string{"update tg-live/dev/vpc", "close tg-live/prod/iam"}, ops(res))
	assert.Equal(t, []string{
		"PATCH " + ghRepo + "/issues/1", "POST " + ghRepo + "/issues/2/comments", "PATCH " + ghRepo + "/issues/2",
	}, f.writes())
	closed := f.issues[1]
	assert.Equal(t, "closed", closed.State)
	assert.Equal(t, "completed", closed.Reason)
	assert.Contains(t, closed.Comments[0], "Resolved")
	assert.Contains(t, f.issues[0].Body, "aws_subnet.a")

	res, err = g.Publish(ctx, second)
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged tg-live/dev/vpc"}, ops(res))
	assert.Empty(t, f.writes())

	// A run that covers only prod leaves the dev issue open.
	res, err = g.Publish(ctx, driftReport(unit("tg-live/prod/vpc")))
	require.NoError(t, err)
	assert.Empty(t, ops(res))
	assert.Contains(t, f.open(), "tfcensus: drift in tg-live/dev/vpc")
}

func TestGitHubMaxOpenAndSummary(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFakeGitHub(t)
	g := newGitHub(t, f, func(c *notify.GitHubConfig) { c.MaxOpen = 1; c.Summary = true })

	r := driftReport(unit("a", change("x.a")), unit("b", change("x.b")), unit("c"))
	res, err := g.Publish(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{"create a", "skipped b", "create _summary"}, ops(res))
	summary := f.open()["tfcensus: drift summary"]
	require.NotNil(t, summary)
	assert.Contains(t, summary.Body, "no issue (max_open reached)")
	f.writes()

	res, err = g.Publish(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged a", "skipped b", "unchanged _summary"}, ops(res))
	assert.Empty(t, f.writes())
}

func TestGitHubClosesDuplicates(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFakeGitHub(t)
	g := newGitHub(t, f, nil)
	r := driftReport(unit("a", change("x.a")))
	_, err := g.Publish(ctx, r)
	require.NoError(t, err)
	// A second issue with the same marker, as a retried create could leave.
	f.issues = append(f.issues, &fakeIssue{
		Number: 2, Title: f.issues[0].Title, Body: f.issues[0].Body,
		State: "open", Labels: f.issues[0].Labels,
	})
	f.writes()

	res, err := g.Publish(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{"close a", "unchanged a"}, ops(res))
	assert.Equal(t, "duplicate", f.issues[1].Reason)
}

func TestGitHubErrors(t *testing.T) {
	t.Parallel()
	f := newFakeGitHub(t)
	g := newGitHub(t, f, func(c *notify.GitHubConfig) { c.Token = "wrong" })
	_, err := g.Publish(t.Context(), driftReport(unit("a", change("x.a"))))
	require.ErrorIs(t, err, notify.ErrAuth)
	assert.NotContains(t, err.Error(), "wrong")

	_, err = notify.NewGitHub(client(t), notify.GitHubConfig{Repo: "acme/infra-live"})
	require.ErrorIs(t, err, notify.ErrConfig)
	_, err = notify.NewGitHub(client(t), notify.GitHubConfig{Token: "t"})
	require.ErrorIs(t, err, notify.ErrConfig)
}

func TestGitHubWritesAreSpaced(t *testing.T) {
	t.Parallel()
	f := newFakeGitHub(t)
	var pauses []time.Duration
	g := newGitHub(t, f, func(c *notify.GitHubConfig) {
		c.WriteGap = time.Second
		c.Sleep = func(_ context.Context, d time.Duration) error { pauses = append(pauses, d); return nil }
	})
	_, err := g.Publish(t.Context(), driftReport(unit("a", change("x.a")), unit("b", change("x.b"))))
	require.NoError(t, err)
	assert.Len(t, pauses, 4, "one pause before every write after the first (3 labels, 2 issues)")
}

func newBitbucket(t *testing.T, f *fakeBitbucket, dry bool) *notify.Bitbucket {
	t.Helper()
	b, err := notify.NewBitbucket(client(t), notify.BitbucketConfig{
		Workspace:   "acme",
		Repo:        "infra-live",
		PullRequest: 7,
		APIURL:      f.srv.URL + "/2.0",
		Token:       "bb-test-token",
		DryRun:      dry,
	})
	require.NoError(t, err)
	return b
}

func TestBitbucketLifecycle(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	f := newFakeBitbucket(t)
	b := newBitbucket(t, f, false)

	res, err := b.Publish(ctx, driftReport(unit("a")))
	require.NoError(t, err)
	assert.Equal(t, []string{"skipped pull-request"}, ops(res), "nothing to report and no comment yet")

	r := driftReport(unit("a", change("x.a")), unit("b"))
	res, err = b.Publish(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{"create pull-request"}, ops(res))
	assert.Equal(t, []string{"POST " + bbComments}, f.writes())

	res, err = b.Publish(ctx, r)
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged pull-request"}, ops(res))
	assert.Empty(t, f.writes())

	res, err = newBitbucket(t, f, true).Publish(ctx, driftReport(unit("a"), unit("b", change("x.b"))))
	require.NoError(t, err)
	assert.Equal(t, []string{"update pull-request"}, ops(res))
	assert.Empty(t, f.writes())

	res, err = b.Publish(ctx, driftReport(unit("a"), unit("b", change("x.b"))))
	require.NoError(t, err)
	assert.Equal(t, []string{"update pull-request"}, ops(res))
	assert.Equal(t, []string{"PUT " + bbComments + "/2"}, f.writes())

	res, err = b.Publish(ctx, driftReport(unit("a"), unit("b")))
	require.NoError(t, err)
	assert.Equal(t, []string{"close pull-request"}, ops(res))
	ours := f.ours()
	require.Len(t, ours, 1)
	assert.Contains(t, ours[0].Content.Raw, "Nothing to report")
	f.writes()

	res, err = b.Publish(ctx, driftReport(unit("a"), unit("b")))
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged pull-request"}, ops(res))
	assert.Empty(t, f.writes())
}

func TestBitbucketBasicAuth(t *testing.T) {
	t.Parallel()
	var auth atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth.Store(r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"values":[]}`)
	}))
	t.Cleanup(srv.Close)
	b, err := notify.NewBitbucket(client(t), notify.BitbucketConfig{
		Workspace:   "acme",
		Repo:        "infra-live",
		PullRequest: 7,
		APIURL:      srv.URL,
		Token:       "tok",
		Email:       "ci@acme.example",
		DryRun:      true,
	})
	require.NoError(t, err)
	_, err = b.Publish(t.Context(), driftReport(unit("a", change("x.a"))))
	require.NoError(t, err)
	assert.Equal(t, "Basic Y2lAYWNtZS5leGFtcGxlOnRvaw==", auth.Load())

	_, err = notify.NewBitbucket(client(t), notify.BitbucketConfig{Workspace: "acme", Repo: "infra-live", Token: "t"})
	require.ErrorIs(t, err, notify.ErrConfig, "no pull request")
}

func TestSlack(t *testing.T) {
	t.Parallel()
	var got atomic.Value
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		b, _ := io.ReadAll(r.Body)
		got.Store(string(b))
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	hook := srv.URL + "/services/T000/B000/hook-secret"
	r := driftReport(
		unit("a", change("x.a"), change("x.b")),
		notify.Scope{Key: "b", Title: "b", Failed: true, Reason: "plan failed"},
		unit("c"),
	)

	s, err := notify.NewSlack(
		client(t),
		notify.SlackConfig{Webhook: hook, Top: 1, Version: "0.5.0", Link: "https://ci.example/run/1"},
	)
	require.NoError(t, err)
	res, err := s.Publish(t.Context(), r)
	require.NoError(t, err)
	assert.Equal(t, []string{"send all"}, ops(res))
	assert.Equal(t, int32(2), calls.Load(), "a 429 is retried")
	var pretty map[string]any
	require.NoError(t, json.Unmarshal([]byte(got.Load().(string)), &pretty))
	b, err := json.MarshalIndent(pretty, "", "  ")
	require.NoError(t, err)
	testutil.Golden(t, filepath.Join("testdata", "slack.golden.json"), append(b, '\n'))

	res, err = s.Publish(t.Context(), driftReport(unit("a")))
	require.NoError(t, err)
	assert.Equal(t, []string{"skipped all"}, ops(res))
	assert.Equal(t, int32(2), calls.Load(), "when: findings sends nothing for a clean report")

	broken, err := notify.NewSlack(
		client(t),
		notify.SlackConfig{Webhook: "http://127.0.0.1:1/services/T/B/hook-secret"},
	)
	require.NoError(t, err)
	_, err = broken.Publish(t.Context(), r)
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "hook-secret", "the webhook URL is a secret")
}

func TestFile(t *testing.T) {
	t.Parallel()
	dir := filepath.Join(t.TempDir(), "out dir")
	fl := notify.NewFile(notify.FileConfig{Dir: dir})
	r := driftReport(unit("a", change("x.a")), unit("b"))

	res, err := fl.Publish(t.Context(), r)
	require.NoError(t, err)
	assert.Equal(t, []string{"write all", "write all"}, ops(res))
	md, err := os.ReadFile(filepath.Join(dir, "tfcensus-drift.md"))
	require.NoError(t, err)
	assert.Contains(t, string(md), "x.a")
	testutil.Golden(t, filepath.Join("testdata", "file.golden.md"), md)

	res, err = fl.Publish(t.Context(), r)
	require.NoError(t, err)
	assert.Equal(t, []string{"unchanged all", "unchanged all"}, ops(res))

	res, err = notify.NewFile(notify.FileConfig{Dir: dir, Formats: []string{"json"}, DryRun: true}).
		Publish(t.Context(), driftReport(unit("a")))
	require.NoError(t, err)
	assert.Equal(t, []string{"write all"}, ops(res))
	b, err := os.ReadFile(filepath.Join(dir, "tfcensus-drift.json"))
	require.NoError(t, err)
	assert.Contains(t, string(b), "x.a", "a dry run writes nothing")
}

func TestMarker(t *testing.T) {
	t.Parallel()
	m := notify.Marker{Kind: report.KindDrift, Scope: "a--> <b>", Hash: "sha256:1"}
	s := m.String()
	assert.Equal(t, 1, strings.Count(s, "-->"), "the scope can't end the comment")
	got, ok := notify.ParseMarker("text\n" + s + "\nmore")
	require.True(t, ok)
	assert.Equal(t, m, got)
	_, ok = notify.ParseMarker("<!-- tfcensus:v1 {} -->")
	assert.False(t, ok)
}

func TestIssueBodyIsCapped(t *testing.T) {
	t.Parallel()
	sc := unit("a")
	for range 5000 {
		sc.Items = append(sc.Items, notify.Item{
			Level:  "unmanaged",
			Type:   "AWS::EC2::SecurityGroup",
			Target: "arn:aws:ec2:eu-west-1:111111111111:security-group/sg-0123456789abcdef0",
			Detail: strings.Repeat("x", 40),
		})
	}
	body, _ := notify.IssueBody(report.KindScan, &sc)
	assert.LessOrEqual(t, len(body), notify.MaxBody)
	assert.Contains(t, body, "more. Run tfcensus for the full list.")
}
