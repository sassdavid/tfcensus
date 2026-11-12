package httpx_test

import (
	"context"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sassdavid/tfcensus/internal/app"
	"github.com/sassdavid/tfcensus/internal/httpx"
)

func newClient(t *testing.T, srv *httptest.Server, lim httpx.Limit) *http.Client {
	t.Helper()
	c := httpx.New(httpx.Options{
		Logger:       slog.New(slog.DiscardHandler),
		Transport:    srv.Client().Transport,
		UserAgent:    "tfcensus/test",
		Limits:       map[string]httpx.Limit{},
		Default:      lim,
		RetryMax:     3,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	})
	return c
}

func get(ctx context.Context, c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func TestRetriesServerErrors(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "tfcensus/test", r.UserAgent())
		if calls.Add(1) < 3 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	resp, err := get(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(3), calls.Load())
}

func TestGivesUpWithLastResponse(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	resp, err := get(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
	assert.Equal(t, int32(4), calls.Load(), "one attempt plus RetryMax retries")
}

func TestRateLimitExhausted(t *testing.T) {
	t.Parallel()
	reset := time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.Header().Set("X-Ratelimit-Limit", "60")
		w.Header().Set("X-Ratelimit-Remaining", "0")
		w.Header().Set("X-Ratelimit-Reset", strconv.FormatInt(reset.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
	}))
	t.Cleanup(srv.Close)

	_, err := get(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.ErrorIs(t, err, httpx.ErrRateLimited)
	rl, ok := errors.AsType[*httpx.RateLimitError](err)
	require.True(t, ok)
	assert.Equal(t, 60, rl.Limit)
	assert.Contains(t, err.Error(), "resets at 12:00:00 UTC")
	assert.Equal(t, int32(1), calls.Load(), "an exhausted limit is not retried")
}

func TestSecondaryRateLimitIsRetried(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)

	resp, err := get(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestConcurrencyLimitPerHost(t *testing.T) {
	t.Parallel()
	const requests, limit = 8, 2
	var inFlight, peak atomic.Int32
	arrived := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		n := inFlight.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		arrived <- struct{}{}
		<-release
		inFlight.Add(-1)
	}))
	t.Cleanup(srv.Close)
	c := newClient(t, srv, httpx.Limit{Concurrency: limit})

	var wg sync.WaitGroup
	for range requests {
		wg.Go(func() {
			resp, err := get(t.Context(), c, srv.URL)
			if assert.NoError(t, err) {
				_ = resp.Body.Close()
			}
		})
	}
	for range requests {
		<-arrived
		release <- struct{}{}
	}
	wg.Wait()
	assert.LessOrEqual(t, peak.Load(), int32(limit))
}

func TestCancelWhileWaiting(t *testing.T) {
	t.Parallel()
	arrived := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		arrived <- struct{}{}
		<-r.Context().Done()
	}))
	t.Cleanup(srv.Close)
	c := newClient(t, srv, httpx.Limit{Concurrency: 1})

	ctx, cancel := context.WithCancel(t.Context())
	errs := make(chan error, 2)
	for range 2 { // the second request waits for the only slot
		go func() {
			resp, err := get(ctx, c, srv.URL)
			if err == nil {
				_ = resp.Body.Close()
			}
			errs <- err
		}()
	}
	<-arrived
	cancel()
	for range 2 {
		require.ErrorIs(t, <-errs, context.Canceled)
	}
}

func post(ctx context.Context, c *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, strings.NewReader("{}"))
	if err != nil {
		return nil, err
	}
	return c.Do(req)
}

func TestPostIsNotRetriedAfterServerError(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)

	resp, err := post(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusBadGateway, resp.StatusCode)
	assert.Equal(t, int32(1), calls.Load(), "the server may have created something")
}

func TestPostIsRetriedAfterTooManyRequests(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.WriteHeader(http.StatusCreated)
	}))
	t.Cleanup(srv.Close)

	resp, err := post(t.Context(), newClient(t, srv, httpx.Limit{Concurrency: 1}), srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusCreated, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load())
}

func TestNearLimitSlowsDown(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Ratelimit-Limit", "1000")
		w.Header().Set("X-Ratelimit-Nearlimit", "true")
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	var logs strings.Builder
	c := httpx.New(httpx.Options{
		Logger:    slog.New(slog.NewTextHandler(&logs, nil)),
		Transport: srv.Client().Transport,
		Limits:    map[string]httpx.Limit{},
		Default:   httpx.Limit{Concurrency: 1, Rate: 100},
	})

	for range 2 {
		resp, getErr := get(t.Context(), c, srv.URL)
		require.NoError(t, getErr)
		_ = resp.Body.Close()
	}
	assert.Equal(t, 1, strings.Count(logs.String(), "rate limit almost used up"), "warns once per host")
}

func TestCAFile(t *testing.T) {
	t.Parallel()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	block := &pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}
	require.NoError(t, os.WriteFile(ca, pem.EncodeToMemory(block), 0o600))

	rt, err := httpx.CATransport(ca)
	require.NoError(t, err)
	c := httpx.New(httpx.Options{Logger: slog.New(slog.DiscardHandler), Transport: rt})
	resp, err := get(t.Context(), c, srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	_, err = httpx.CATransport(filepath.Join(t.TempDir(), "missing.pem"))
	require.Error(t, err)
}

func TestRetryPOST(t *testing.T) {
	t.Parallel()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	c := httpx.New(httpx.Options{
		Logger:       slog.New(slog.DiscardHandler),
		Transport:    srv.Client().Transport,
		RetryPOST:    true,
		RetryWaitMin: time.Millisecond,
		RetryWaitMax: 2 * time.Millisecond,
	})

	resp, err := post(t.Context(), c, srv.URL)
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, int32(2), calls.Load(), "a POST that only reads is resent")
}

func TestSecretPathHostsAreRedacted(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	host, _, err := net.SplitHostPort(strings.TrimPrefix(srv.URL, "http://"))
	require.NoError(t, err)
	var logs strings.Builder
	c := httpx.New(httpx.Options{
		Logger:          slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: app.LevelTrace})),
		Transport:       srv.Client().Transport,
		SecretPathHosts: []string{host},
	})

	resp, err := post(t.Context(), c, srv.URL+"/services/T000/B000/secret")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotContains(t, logs.String(), "secret")
	assert.Contains(t, logs.String(), "[redacted]")
}
