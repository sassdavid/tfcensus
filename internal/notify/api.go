package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const maxErrorBody = 300

// api sends JSON requests with one authorization header.
type api struct {
	hc    *http.Client
	auth  string // Authorization header value
	extra map[string]string
	// gap is the pause between writes; GitHub asks for at least a second.
	gap       time.Duration
	sleep     func(ctx context.Context, d time.Duration) error
	lastWrite bool
}

// statusError is a response outside 2xx.
type statusError struct {
	method, path string
	status       int
	body         string
}

func (e *statusError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d: %s", e.method, e.path, e.status, e.body)
}

func (e *statusError) Unwrap() error {
	if e.status == http.StatusUnauthorized || e.status == http.StatusForbidden {
		return ErrAuth
	}
	return nil
}

// do sends a request to url with body as JSON and decodes a 2xx response into out. It returns the
// response headers, for pagination.
func (a *api) do(ctx context.Context, method, url string, body, out any) (http.Header, error) {
	if method != http.MethodGet {
		if a.lastWrite && a.gap > 0 {
			if err := a.sleep(ctx, a.gap); err != nil {
				return nil, err
			}
		}
		a.lastWrite = true
	}
	var rd io.Reader = http.NoBody
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if a.auth != "" {
		req.Header.Set("Authorization", a.auth)
	}
	for k, v := range a.extra {
		req.Header.Set(k, v)
	}
	resp, err := a.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, pathOf(url), err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		return resp.Header, &statusError{
			method: method, path: pathOf(url), status: resp.StatusCode, body: strings.TrimSpace(string(b)),
		}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return resp.Header, nil
	}
	if err = json.NewDecoder(resp.Body).Decode(out); err != nil {
		return resp.Header, fmt.Errorf("%s %s: decode response: %w", method, pathOf(url), err)
	}
	return resp.Header, nil
}

// pathOf drops the scheme, host and query from a URL for messages.
func pathOf(url string) string {
	if i := strings.Index(url, "://"); i >= 0 {
		url = url[i+3:]
		if j := strings.IndexByte(url, '/'); j >= 0 {
			url = url[j:]
		}
	}
	if i := strings.IndexByte(url, '?'); i >= 0 {
		url = url[:i]
	}
	return url
}

// SleepContext waits for d or until ctx is done.
func SleepContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err() //nolint:wrapcheck // the caller matches context errors
	}
}
