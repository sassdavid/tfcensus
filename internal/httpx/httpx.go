package httpx

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"
	"golang.org/x/time/rate"

	"github.com/sassdavid/tfcensus/internal/app"
)

// GitHubAPIHost is the host of the GitHub REST and GraphQL APIs.
const GitHubAPIHost = "api.github.com"

const (
	defaultRetryMax     = 4
	defaultRetryWaitMin = time.Second
	defaultRetryWaitMax = 30 * time.Second
	attemptTimeout      = 2 * time.Minute
	lowRemainingDivisor = 10 // slow down below 10% of the rate limit
	minLowRate          = 0.2
	nearLimitRate       = 1 // requests per second once a host says it is near its limit
	githubConcurrency   = 8
	githubRate          = 20
	otherConcurrency    = 4
	otherRate           = 10
)

// ErrRateLimited is wrapped by [RateLimitError].
var ErrRateLimited = errors.New("rate limit reached")

// RateLimitError says that a host refused a request because its rate limit is used up.
type RateLimitError struct {
	Host  string
	Limit int
	Reset time.Time
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("%s rate limit reached (%d requests per hour, resets at %s)",
		e.Host, e.Limit, e.Reset.UTC().Format(time.TimeOnly+" UTC"))
}

// Is makes [errors.Is](err, [ErrRateLimited]) true.
func (e *RateLimitError) Is(target error) bool { return target == ErrRateLimited }

// Limit is the request budget for one host.
type Limit struct {
	Concurrency int     // requests in flight at once
	Rate        float64 // requests per second; 0 means no rate limit
}

// Options configures [New].
type Options struct {
	Logger       *slog.Logger
	Transport    http.RoundTripper // nil means a pooled transport that honours proxy variables
	UserAgent    string
	Limits       map[string]Limit // by host
	Default      Limit            // hosts not in Limits
	RetryMax     int
	RetryWaitMin time.Duration
	RetryWaitMax time.Duration
	Clock        func() time.Time
	RetryPOST    bool                                      // POSTs are reads, such as GraphQL queries, and may be resent
	Wrap         func(http.RoundTripper) http.RoundTripper // outermost layer, such as a cache; nil means none
	// SecretPathHosts are hosts whose URL path is a secret, such as hooks.slack.com; logs show
	// their paths as [redacted].
	SecretPathHosts []string
}

// DefaultLimits returns the per-host limits: 8 concurrent requests to the GitHub API, 4 elsewhere.
func DefaultLimits() (map[string]Limit, Limit) {
	return map[string]Limit{GitHubAPIHost: {Concurrency: githubConcurrency, Rate: githubRate}},
		Limit{Concurrency: otherConcurrency, Rate: otherRate}
}

// New returns a client that retries, limits and logs every request. Without o.RetryPOST, a POST is
// retried only when the server refused it with 429 or a Retry-After, so a create is never sent twice.
func New(o Options) *http.Client {
	if o.Transport == nil {
		o.Transport = defaultTransport()
	}
	if o.Limits == nil {
		o.Limits, o.Default = DefaultLimits()
	}
	if o.RetryMax == 0 {
		o.RetryMax = defaultRetryMax
	}
	if o.RetryWaitMin == 0 {
		o.RetryWaitMin = defaultRetryWaitMin
	}
	if o.RetryWaitMax == 0 {
		o.RetryWaitMax = defaultRetryWaitMax
	}
	if o.Clock == nil {
		o.Clock = time.Now
	}
	inner := &logging{
		logger: o.Logger, userAgent: o.UserAgent, retryPOST: o.RetryPOST, secretHosts: o.SecretPathHosts,
		next: o.Transport,
	}
	lim := &limiter{
		logger: o.Logger,
		limits: o.Limits,
		def:    o.Default,
		clock:  o.Clock,
		hosts:  map[string]*hostLimit{},
		next:   inner,
	}
	rc := &retryablehttp.Client{
		HTTPClient:   &http.Client{Transport: lim, Timeout: attemptTimeout},
		RetryWaitMin: o.RetryWaitMin,
		RetryWaitMax: o.RetryWaitMax,
		RetryMax:     o.RetryMax,
		CheckRetry:   retryPolicy(o.RetryPOST),
		Backoff:      backoff,
		ErrorHandler: retryablehttp.PassthroughErrorHandler,
	}
	var rt http.RoundTripper = &retryablehttp.RoundTripper{Client: rc}
	if o.Wrap != nil {
		rt = o.Wrap(rt)
	}
	return &http.Client{Transport: rt}
}

// defaultTransport clones the default transport, which keeps ProxyFromEnvironment and the default
// timeouts.
func defaultTransport() http.RoundTripper {
	if t, ok := http.DefaultTransport.(*http.Transport); ok {
		return t.Clone()
	}
	return http.DefaultTransport
}

// CATransport returns the default transport trusting the PEM certificates in caFile on top of the
// system pool, for [Options.Transport]. SSL_CERT_FILE works this way on macOS and Windows too.
func CATransport(caFile string) (http.RoundTripper, error) {
	t, ok := defaultTransport().(*http.Transport)
	if !ok {
		return nil, errors.New("default transport is not an *http.Transport")
	}
	pem, err := os.ReadFile(caFile)
	if err != nil {
		return nil, fmt.Errorf("read CA bundle: %w", err)
	}
	pool, err := x509.SystemCertPool()
	if err != nil {
		pool = x509.NewCertPool()
	}
	if !pool.AppendCertsFromPEM(pem) {
		return nil, fmt.Errorf("read CA bundle %s: no PEM certificates", caFile)
	}
	t.TLSClientConfig = &tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12}
	return t, nil
}

// noRetryError marks a failed POST: the server may have acted on it, so it isn't sent again.
type noRetryError struct{ err error }

func (e *noRetryError) Error() string { return e.err.Error() }
func (e *noRetryError) Unwrap() error { return e.err }

func retryPolicy(retryPOST bool) retryablehttp.CheckRetry {
	return func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if !retryPOST && err == nil && resp.Request != nil && resp.Request.Method == http.MethodPost &&
			resp.StatusCode != http.StatusTooManyRequests && resp.Header.Get("Retry-After") == "" {
			return false, nil
		}
		return checkRetry(ctx, resp, err)
	}
}

func checkRetry(ctx context.Context, resp *http.Response, err error) (bool, error) {
	if ctx.Err() != nil {
		return false, ctx.Err() //nolint:wrapcheck // the caller matches context errors
	}
	if errors.Is(err, ErrRateLimited) {
		return false, err
	}
	if _, ok := errors.AsType[*noRetryError](err); ok {
		return false, err
	}
	if err == nil && resp.Header.Get("Retry-After") != "" &&
		(resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests) {
		return true, nil // GitHub answers a secondary rate limit with 403 and Retry-After
	}
	return retryablehttp.DefaultRetryPolicy(ctx, resp, err) //nolint:wrapcheck // passes the policy's verdict through
}

// backoff honours Retry-After and otherwise waits a random time up to min*2^attempt (full jitter).
func backoff(minWait, maxWait time.Duration, attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if s, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && s >= 0 {
			return min(time.Duration(s)*time.Second, maxWait)
		}
	}
	ceiling := min(minWait<<min(attempt, 30), maxWait) //nolint:mnd // cap the shift before it overflows
	if ceiling <= minWait {
		return minWait
	}
	return minWait + rand.N(ceiling-minWait) //nolint:gosec // jitter needs no cryptographic randomness
}

type hostLimit struct {
	sem  chan struct{}
	rate *rate.Limiter // nil when the host has no rate limit
	low  bool          // the low-remaining warning was logged
}

// limiter enforces the per-host limits on every attempt, so a request waiting for a retry holds
// no slot.
type limiter struct {
	logger *slog.Logger
	limits map[string]Limit
	def    Limit
	clock  func() time.Time
	next   http.RoundTripper

	mu    sync.Mutex
	hosts map[string]*hostLimit
}

func (l *limiter) host(name string) *hostLimit {
	l.mu.Lock()
	defer l.mu.Unlock()
	if h, ok := l.hosts[name]; ok {
		return h
	}
	lim, ok := l.limits[name]
	if !ok {
		lim = l.def
	}
	h := &hostLimit{sem: make(chan struct{}, max(lim.Concurrency, 1))}
	if lim.Rate > 0 {
		h.rate = rate.NewLimiter(rate.Limit(lim.Rate), max(lim.Concurrency, 1))
	}
	l.hosts[name] = h
	return h
}

func (l *limiter) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	h := l.host(req.URL.Host)
	select {
	case h.sem <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err() //nolint:wrapcheck // the caller matches context errors
	}
	defer func() { <-h.sem }()
	if h.rate != nil {
		if err := h.rate.Wait(ctx); err != nil {
			return nil, fmt.Errorf("wait for %s rate limit: %w", req.URL.Host, err)
		}
	}
	resp, err := l.next.RoundTrip(req)
	if err != nil {
		return nil, err //nolint:wrapcheck // a transport passes errors through unchanged
	}
	if rlErr := l.watch(ctx, req.URL.Host, h, resp); rlErr != nil {
		_ = resp.Body.Close()
		return nil, rlErr
	}
	return resp, nil
}

// watch reads the x-ratelimit headers of GitHub and Bitbucket Cloud. It turns an exhausted limit into a [RateLimitError]
// and spreads the remaining requests until the reset when less than 10% is left.
func (l *limiter) watch(ctx context.Context, host string, h *hostLimit, resp *http.Response) error {
	if resp.Header.Get("X-Ratelimit-Nearlimit") == "true" && h.rate != nil {
		// Bitbucket Cloud sends no remaining count, only this flag below 20% left.
		h.rate.SetLimit(rate.Limit(min(nearLimitRate, float64(h.rate.Limit()))))
		l.warnLow(ctx, h, "host", host, "limit", resp.Header.Get("X-Ratelimit-Limit"))
		return nil
	}
	remaining, limit, reset, ok := rateLimitHeaders(resp.Header)
	if !ok {
		return nil
	}
	limited := resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests
	if remaining == 0 && limited {
		return &RateLimitError{Host: host, Limit: limit, Reset: reset}
	}
	if remaining >= limit/lowRemainingDivisor || h.rate == nil {
		return nil
	}
	perSecond := minLowRate
	if until := reset.Sub(l.clock()).Seconds(); until > 0 {
		perSecond = max(float64(remaining)/until, minLowRate)
	}
	h.rate.SetLimit(rate.Limit(min(perSecond, float64(h.rate.Limit()))))
	l.warnLow(ctx, h, "host", host, "remaining", remaining, "limit", limit, "reset", reset.UTC().Format(time.RFC3339))
	return nil
}

// warnLow logs once per host that the rate limit is almost used up.
func (l *limiter) warnLow(ctx context.Context, h *hostLimit, attrs ...any) {
	l.mu.Lock()
	first := !h.low
	h.low = true
	l.mu.Unlock()
	if first {
		l.logger.WarnContext(ctx, "rate limit almost used up; slowing down", attrs...)
	}
}

// rateLimitHeaders parses x-ratelimit-remaining, -limit and -reset; ok is false when any is missing.
func rateLimitHeaders(h http.Header) (int, int, time.Time, bool) {
	remaining, err := strconv.Atoi(h.Get("X-Ratelimit-Remaining"))
	if err != nil {
		return 0, 0, time.Time{}, false
	}
	limit, err := strconv.Atoi(h.Get("X-Ratelimit-Limit"))
	if err != nil || limit <= 0 {
		return 0, 0, time.Time{}, false
	}
	resetUnix, err := strconv.ParseInt(h.Get("X-Ratelimit-Reset"), 10, 64)
	if err != nil {
		return 0, 0, time.Time{}, false
	}
	return remaining, limit, time.Unix(resetUnix, 0), true
}

// logging sets the User-Agent and logs each attempt: a line at debug, headers at trace.
type logging struct {
	logger      *slog.Logger
	userAgent   string
	retryPOST   bool
	secretHosts []string
	next        http.RoundTripper
}

func (l *logging) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := req.Context()
	out := req.Clone(ctx)
	if l.userAgent != "" {
		out.Header.Set("User-Agent", l.userAgent)
	}
	start := time.Now()
	resp, err := l.next.RoundTrip(out)
	shown := req.URL.String()
	if slices.Contains(l.secretHosts, req.URL.Hostname()) {
		shown = req.URL.Scheme + "://" + req.URL.Host + "/[redacted]"
	}
	attrs := []any{"method", req.Method, "url", shown, "duration", time.Since(start).Round(time.Millisecond)}
	if err != nil {
		errText := err.Error()
		if shown != req.URL.String() {
			errText = strings.ReplaceAll(errText, req.URL.String(), shown)
		}
		l.logger.DebugContext(ctx, "http request failed", append(attrs, "error", errText)...)
		if req.Method == http.MethodPost && !l.retryPOST {
			return nil, &noRetryError{err: err}
		}
		return nil, err //nolint:wrapcheck // a transport passes errors through unchanged
	}
	l.logger.DebugContext(ctx, "http request", append(attrs, "status", resp.StatusCode)...)
	if l.logger.Enabled(ctx, app.LevelTrace) {
		l.logger.Log(ctx, app.LevelTrace, "http exchange",
			"request_headers", redact(out.Header), "response_headers", redact(resp.Header))
	}
	return resp, nil
}

func redact(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, vs := range h {
		v := fmt.Sprint(vs)
		switch http.CanonicalHeaderKey(k) {
		case "Authorization", "Proxy-Authorization", "Cookie", "Set-Cookie", "X-Amz-Security-Token":
			v = "[redacted]"
		}
		out[k] = v
	}
	return out
}
