package vcr_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"

	"github.com/sassdavid/tfcensus/internal/testutil/vcr"
)

const secret = "s3cr3t-token-value"

func interaction() *cassette.Interaction {
	return &cassette.Interaction{
		Request: cassette.Request{
			Method:  http.MethodPost,
			URL:     "https://api.example.test/v1?key=" + secret,
			Body:    `{"token":"` + secret + `","account":"123456789012"}`,
			Headers: http.Header{"Authorization": {"Bearer " + secret}, "X-Custom": {"keep " + secret}},
		},
		Response: cassette.Response{
			Body:    "account 123456789012 ok",
			Headers: http.Header{"Set-Cookie": {"session=abc"}, "X-Session-Token": {"tok"}},
		},
	}
}

func TestScrub(t *testing.T) {
	t.Parallel()
	s := vcr.Scrubber{
		Headers: []string{"X-Session-Token"},
		Secrets: []string{"", secret},
		Replace: []vcr.Replacement{{Pattern: regexp.MustCompile(`\b\d{12}\b`), With: "111111111111"}},
	}
	i := interaction()
	require.NoError(t, s.Scrub(i))

	assert.Empty(t, i.Request.Headers.Get("Authorization"))
	assert.Empty(t, i.Response.Headers.Get("Set-Cookie"))
	assert.Empty(t, i.Response.Headers.Get("X-Session-Token"))
	assert.Equal(t, "keep "+vcr.Redacted, i.Request.Headers.Get("X-Custom"))
	assert.Equal(t, "https://api.example.test/v1?key="+vcr.Redacted, i.Request.URL)
	assert.JSONEq(t, `{"token":"REDACTED","account":"111111111111"}`, i.Request.Body)
	assert.Equal(t, int64(len(i.Request.Body)), i.Request.ContentLength)
	assert.Equal(t, "account 111111111111 ok", i.Response.Body)
}

func TestScrubRefusesLeftoverSecret(t *testing.T) {
	t.Parallel()
	// A replacement that puts the secret back simulates a value the scrubber can't remove.
	s := vcr.Scrubber{
		Secrets: []string{secret},
		Replace: []vcr.Replacement{{Pattern: regexp.MustCompile(vcr.Redacted), With: secret}},
	}
	err := s.Scrub(interaction())
	require.ErrorIs(t, err, vcr.ErrSecretLeft)
}

func TestMatcher(t *testing.T) {
	t.Parallel()
	s := vcr.Scrubber{Secrets: []string{secret}, Match: []string{"X-Target"}}
	recorded := cassette.Request{
		Method:  http.MethodPost,
		URL:     "https://h.test/q?key=REDACTED",
		Body:    `{"a":1}`,
		Headers: http.Header{"X-Target": {"Describe"}},
	}
	tests := []struct {
		name   string
		method string
		url    string
		body   string
		target string
		want   bool
	}{
		{"same after scrubbing", http.MethodPost, "https://h.test/q?key=" + secret, `{"a":1}`, "Describe", true},
		{"other body", http.MethodPost, "https://h.test/q?key=" + secret, `{"a":2}`, "Describe", false},
		{"other method", http.MethodGet, "https://h.test/q?key=" + secret, `{"a":1}`, "Describe", false},
		{"other url", http.MethodPost, "https://h.test/r", `{"a":1}`, "Describe", false},
		{"other matched header", http.MethodPost, "https://h.test/q?key=" + secret, `{"a":1}`, "List", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequestWithContext(t.Context(), tt.method, tt.url, strings.NewReader(tt.body))
			req.Header.Set("Authorization", "Bearer "+secret)
			req.Header.Set("X-Target", tt.target)
			assert.Equal(t, tt.want, s.Matcher()(req, recorded))
			// The body stays readable for the real transport.
			b, err := io.ReadAll(req.Body)
			require.NoError(t, err)
			assert.Equal(t, tt.body, string(b))
		})
	}
}

func TestRecordThenReplay(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "hello "+r.URL.Query().Get("name"))
	}))
	t.Cleanup(srv.Close)
	path := filepath.Join(t.TempDir(), "greeting.yaml")
	s := vcr.Scrubber{Secrets: []string{secret}}

	rec, err := vcr.New(path, vcr.Options{Record: true, Real: srv.Client().Transport, Scrubber: s})
	require.NoError(t, err)
	assert.Equal(t, "hello world", get(t, rec, srv.URL+"/?name=world", secret))
	require.NoError(t, rec.Stop())

	saved, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.NotContains(t, string(saved), secret)
	assert.NotContains(t, string(saved), "Authorization")

	srv.Close()
	replay, err := vcr.New(path, vcr.Options{Scrubber: s})
	require.NoError(t, err)
	assert.Equal(t, "hello world", get(t, replay, srv.URL+"/?name=world", "another-token"))
}

func TestReplayMissingCassette(t *testing.T) {
	t.Parallel()
	_, err := vcr.New(filepath.Join(t.TempDir(), "missing"), vcr.Options{})
	require.ErrorContains(t, err, vcr.RecordEnv+"=1")
}

func get(t *testing.T, rt http.RoundTripper, url, token string) string {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := rt.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}
