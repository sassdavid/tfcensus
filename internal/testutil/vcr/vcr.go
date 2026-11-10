package vcr

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
	"testing"

	"gopkg.in/dnaeon/go-vcr.v4/pkg/cassette"
	"gopkg.in/dnaeon/go-vcr.v4/pkg/recorder"
)

// RecordEnv makes [ForTest] record against the real service. VCR_RECORD=1 go test ./... sets it.
const RecordEnv = "VCR_RECORD"

// Redacted replaces every secret value in a saved cassette.
const Redacted = "REDACTED"

const cassetteExt = ".yaml"

// ErrSecretLeft is returned when a secret is still in an interaction after scrubbing.
var ErrSecretLeft = errors.New("secret left in cassette")

// Recording reports whether cassettes should be recorded instead of replayed.
func Recording() bool {
	return os.Getenv(RecordEnv) != ""
}

// DefaultHeaders lists the headers a [Scrubber] always drops from requests and responses.
func DefaultHeaders() []string {
	return []string{"Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie"}
}

// Replacement rewrites every match of Pattern with With.
type Replacement struct {
	Pattern *regexp.Regexp
	With    string
}

// Scrubber removes credentials from interactions before they are saved.
type Scrubber struct {
	Headers []string      // dropped in addition to [DefaultHeaders]
	Secrets []string      // literal values replaced with [Redacted]; empty strings are ignored
	Replace []Replacement // applied after Secrets, for values such as account IDs
	Match   []string      // request headers the matcher compares, such as X-Amz-Target
}

// Scrub cleans i in place and fails with [ErrSecretLeft] if a secret survives.
func (s Scrubber) Scrub(i *cassette.Interaction) error {
	for _, h := range append(DefaultHeaders(), s.Headers...) {
		i.Request.Headers.Del(h)
		i.Response.Headers.Del(h)
	}
	s.scrubHeader(i.Request.Headers)
	s.scrubHeader(i.Response.Headers)
	s.scrubHeader(http.Header(i.Request.Form))
	i.Request.URL = s.text(i.Request.URL)
	i.Request.RequestURI = s.text(i.Request.RequestURI)
	if body := s.text(i.Request.Body); body != i.Request.Body {
		i.Request.Body = body
		i.Request.ContentLength = int64(len(body))
	}
	if body := s.text(i.Response.Body); body != i.Response.Body {
		i.Response.Body = body
		i.Response.ContentLength = int64(len(body))
	}
	return s.check(i)
}

func (s Scrubber) scrubHeader(h http.Header) {
	for k, vs := range h {
		for n, v := range vs {
			vs[n] = s.text(v)
		}
		h[k] = vs
	}
}

func (s Scrubber) text(v string) string {
	for _, secret := range s.Secrets {
		if secret != "" {
			v = strings.ReplaceAll(v, secret, Redacted)
		}
	}
	for _, r := range s.Replace {
		v = r.Pattern.ReplaceAllString(v, r.With)
	}
	return v
}

func (s Scrubber) check(i *cassette.Interaction) error {
	fields := []string{i.Request.URL, i.Request.RequestURI, i.Request.Body, i.Response.Body}
	for _, h := range []http.Header{i.Request.Headers, i.Response.Headers, http.Header(i.Request.Form)} {
		for k, vs := range h {
			fields = append(fields, k)
			fields = append(fields, vs...)
		}
	}
	for _, secret := range s.Secrets {
		if secret == "" {
			continue
		}
		for _, f := range fields {
			if strings.Contains(f, secret) {
				return fmt.Errorf("%w: interaction %d (%s %s)", ErrSecretLeft, i.ID, i.Request.Method, i.Request.URL)
			}
		}
	}
	return nil
}

// Matcher matches a request with a recorded one by method, URL and body after scrubbing the
// request the same way, so recorded tokens in URLs still match. Headers are ignored, except those
// in Match: the others carry credentials and client versions.
func (s Scrubber) Matcher() recorder.MatcherFunc {
	return func(r *http.Request, i cassette.Request) bool {
		if r.Method != i.Method || s.text(r.URL.String()) != i.URL {
			return false
		}
		for _, h := range s.Match {
			if r.Header.Get(h) != i.Headers.Get(h) {
				return false
			}
		}
		body, err := readBody(r)
		if err != nil {
			return false
		}
		return s.text(body) == i.Body
	}
}

func readBody(r *http.Request) (string, error) {
	if r.Body == nil || r.Body == http.NoBody {
		return "", nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return "", fmt.Errorf("read request body: %w", err)
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return string(b), nil
}

// Options configures [New].
type Options struct {
	Record   bool              // record against Real and overwrite the cassette
	Real     http.RoundTripper // used when recording; nil means [http.DefaultTransport]
	Scrubber Scrubber
}

// New returns a recorder for the cassette at path (with or without the .yaml extension). It
// replays unless o.Record is set; replaying a missing cassette fails. Call Stop to save a recording.
func New(path string, o Options) (*recorder.Recorder, error) {
	name := strings.TrimSuffix(path, cassetteExt)
	mode := recorder.ModeReplayOnly
	if o.Record {
		mode = recorder.ModeRecordOnly
	} else if _, err := os.Stat(name + cassetteExt); err != nil {
		return nil, fmt.Errorf("open cassette %s%s: %w (record it with %s=1)", name, cassetteExt, err, RecordEnv)
	}
	opts := []recorder.Option{
		recorder.WithMode(mode),
		recorder.WithMatcher(o.Scrubber.Matcher()),
		recorder.WithSkipRequestLatency(true),
		recorder.WithHook(o.Scrubber.Scrub, recorder.BeforeSaveHook),
	}
	if o.Real != nil {
		opts = append(opts, recorder.WithRealTransport(o.Real))
	}
	r, err := recorder.New(name, opts...)
	if err != nil {
		return nil, fmt.Errorf("open cassette %s%s: %w", name, cassetteExt, err)
	}
	return r, nil
}

// ForTest returns a transport for the cassette at path that records when [Recording] is true and
// replays otherwise. The recording is saved when the test ends.
func ForTest(t testing.TB, path string, s Scrubber) http.RoundTripper {
	t.Helper()
	r, err := New(path, Options{Record: Recording(), Scrubber: s})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if stopErr := r.Stop(); stopErr != nil {
			t.Errorf("save cassette %s: %v", path, stopErr)
		}
	})
	return r
}
