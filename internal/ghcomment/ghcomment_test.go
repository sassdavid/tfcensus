package ghcomment_test

import (
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/ghcomment"
	"github.com/sassdavid/tfcensus/internal/testutil/vcr"
)

const cassettes = "../../testdata/cassettes/github"

// counter counts the requests that write.
type counter struct {
	rt     http.RoundTripper
	writes int
}

func (c *counter) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Method != http.MethodGet {
		c.writes++
	}
	return c.rt.RoundTrip(r) //nolint:wrapcheck // a test transport
}

func client(t *testing.T, cassette string, dryRun bool) (*ghcomment.Client, *counter) {
	t.Helper()
	cnt := &counter{rt: vcr.ForTest(t, filepath.Join(cassettes, cassette), vcr.Scrubber{})}
	c, err := ghcomment.New(&http.Client{Transport: cnt}, ghcomment.Config{
		Repo: "sassdavid/infra-live", PullRequest: 42, Token: "test-token", DryRun: dryRun,
	})
	require.NoError(t, err)
	return c, cnt
}

func drift(body string, open bool) ghcomment.Post {
	return ghcomment.Post{Kind: "drift", Title: "Drift", Body: body, Open: open, Hint: "Run with --format json."}
}

// TestPublish replays the comment lifecycle: create, unchanged (no write), update and resolve.
func TestPublish(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cassette string
		post     ghcomment.Post
		dryRun   bool
		op       ghcomment.Op
		writes   int
		url      string
	}{
		{
			"pr-comment-create", drift("2 drifted resources.\n", true), false, ghcomment.OpCreate, 1,
			"https://github.com/sassdavid/infra-live/pull/42#issuecomment-1001",
		},
		{
			"pr-comment-unchanged", drift("2 drifted resources.\n", true), false, ghcomment.OpUnchanged, 0,
			"https://github.com/sassdavid/infra-live/pull/42#issuecomment-1001",
		},
		{
			"pr-comment-update", drift("3 drifted resources.\n", true), false, ghcomment.OpUpdate, 1,
			"https://github.com/sassdavid/infra-live/pull/42#issuecomment-1001",
		},
		{
			"pr-comment-update", drift("3 drifted resources.\n", true), true, ghcomment.OpUpdate, 0,
			"https://github.com/sassdavid/infra-live/pull/42#issuecomment-1001",
		},
		{
			"pr-comment-resolve", drift("", false), false, ghcomment.OpResolve, 1,
			"https://github.com/sassdavid/infra-live/pull/42#issuecomment-1001",
		},
		{"pr-comment-skip", drift("", false), false, ghcomment.OpSkipped, 0, ""},
	}
	for _, tt := range tests {
		t.Run(string(tt.op), func(t *testing.T) {
			t.Parallel()
			c, cnt := client(t, tt.cassette, tt.dryRun)
			res, err := c.Publish(t.Context(), tt.post)
			require.NoError(t, err)
			assert.Equal(t, tt.op, res.Op)
			assert.Equal(t, tt.writes, cnt.writes)
			assert.Equal(t, tt.url, res.URL)
			assert.Equal(t, "sassdavid/infra-live#42", res.Target)
			assert.Equal(t, tt.dryRun, res.DryRun)
		})
	}
}

func TestRender(t *testing.T) {
	t.Parallel()
	c, err := ghcomment.New(http.DefaultClient, ghcomment.Config{
		Repo: "o/r", PullRequest: 1, DryRun: true, MaxChars: 1000,
	})
	require.NoError(t, err)
	body, m := c.Render(drift("| A |\n|---|\n"+strings.Repeat("| a table row |\n", 100), true))
	assert.LessOrEqual(t, len([]rune(body)), 1000)
	assert.Contains(t, body, "Truncated to ")
	assert.Contains(t, body, "Run with --format json.")
	got, ok := ghcomment.ParseMarker(body)
	require.True(t, ok)
	assert.Equal(t, m, got)
	assert.Equal(t, ghcomment.Scope, got.Scope)

	resolved, _ := c.Render(drift("ignored", false))
	assert.Contains(t, resolved, "## Drift\n\nNothing to report.\n")
	assert.NotContains(t, resolved, "ignored")

	dashes := ghcomment.Marker{Kind: "a--b", Scope: ghcomment.Scope, Hash: "h"}
	assert.NotContains(t, strings.TrimSuffix(strings.TrimPrefix(dashes.String(), "<!--"), "-->"), "--")
	back, ok := ghcomment.ParseMarker(dashes.String())
	require.True(t, ok)
	assert.Equal(t, dashes, back)
}

func TestNewChecksConfig(t *testing.T) {
	t.Parallel()
	tests := []struct {
		cfg  ghcomment.Config
		want string
	}{
		{ghcomment.Config{Repo: "norepo", PullRequest: 1, Token: "t"}, `repository "norepo"`},
		{ghcomment.Config{Repo: "o/r/x", PullRequest: 1, Token: "t"}, `repository "o/r/x"`},
		{ghcomment.Config{Repo: "o/r", Token: "t"}, "no pull request number"},
		{ghcomment.Config{Repo: "o/r", PullRequest: 1}, "the token is empty"},
	}
	for _, tt := range tests {
		_, err := ghcomment.New(http.DefaultClient, tt.cfg)
		require.ErrorIs(t, err, ghcomment.ErrConfig)
		require.ErrorContains(t, err, tt.want)
	}
}

type refuse struct{}

func (refuse) RoundTrip(r *http.Request) (*http.Response, error) {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Body:       io.NopCloser(strings.NewReader(`{"message":"Resource not accessible"}`)),
		Header:     http.Header{},
		Request:    r,
	}, nil
}

func TestPublishAuthError(t *testing.T) {
	t.Parallel()
	c, err := ghcomment.New(&http.Client{Transport: refuse{}}, ghcomment.Config{
		Repo: "o/r", PullRequest: 1, Token: "t",
	})
	require.NoError(t, err)
	_, err = c.Publish(t.Context(), drift("x\n", true))
	require.ErrorIs(t, err, ghcomment.ErrAuth)
	require.ErrorContains(t, err, "HTTP 403")
}

func TestFromEnv(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
		return p
	}
	events := []struct {
		name, body string
		want       int
		err        string
	}{
		{"pr.json", `{"number":7,"pull_request":{"number":7}}`, 7, ""},
		{"comment.json", `{"issue":{"number":8,"pull_request":{"url":"x"}}}`, 8, ""},
		{"issue.json", `{"issue":{"number":9}}`, 0, "names no pull request"},
		{"push.json", `{"ref":"refs/heads/main"}`, 0, "names no pull request"},
		{"bad.json", `{`, 0, "parse GITHUB_EVENT_PATH"},
	}
	for _, ev := range events {
		path := write(ev.name, ev.body)
		env := map[string]string{"GITHUB_REPOSITORY": "o/r", "GITHUB_EVENT_PATH": path}
		cfg, err := ghcomment.FromEnv(ghcomment.Config{}, func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		})
		if ev.err != "" {
			require.ErrorIs(t, err, ghcomment.ErrConfig, ev.name)
			require.ErrorContains(t, err, ev.err, ev.name)
			continue
		}
		require.NoError(t, err, ev.name)
		assert.Equal(t, ghcomment.Config{Repo: "o/r", PullRequest: ev.want}, cfg, ev.name)
	}
	cfg, err := ghcomment.FromEnv(ghcomment.Config{Repo: "a/b", PullRequest: 3}, func(string) (string, bool) {
		return "", false
	})
	require.NoError(t, err)
	assert.Equal(t, ghcomment.Config{Repo: "a/b", PullRequest: 3}, cfg)
	_, err = ghcomment.FromEnv(ghcomment.Config{}, func(string) (string, bool) { return "", false })
	require.ErrorContains(t, err, "GITHUB_EVENT_PATH is empty")
}
