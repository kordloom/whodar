// Package httputil holds the shared request loop for whodar's API clients:
// bounded rate-limit retries, a size-limited body read, and Retry-After
// parsing. Each client supplies its own request, auth, and status handling.
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ErrRateLimited indicates the retry budget was spent on rate-limit responses.
// Callers detect it to translate into their own package sentinel.
var ErrRateLimited = errors.New("httputil: rate limited")

// StatusError reports an unexpected HTTP status from an API call. A cheap
// credential check returns it so a caller can map a specific status, such as
// 401 for a bad token, to a specific fix instead of a generic failure.
type StatusError struct {
	// Code is the HTTP status code the server returned.
	Code int
}

// Error describes the unexpected status.
func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected status %d", e.Code)
}

// maxResponseBytes bounds a response body read so a hostile or broken server
// cannot exhaust memory. It sits far above any real API page.
const maxResponseBytes = 64 << 20

// defaultBackoff is the wait between retries when a rate-limited response
// carries no usable Retry-After header.
const defaultBackoff = time.Second

// Doer performs an HTTP request. *http.Client satisfies it; tests inject a stub.
type Doer interface {
	// Do performs the request and returns the response.
	Do(req *http.Request) (*http.Response, error)
}

// Do sends the request built by build, retrying while retryable reports a
// rate-limited response and the budget allows, honoring Retry-After between
// attempts. A nil retryable retries any HTTP 429. On success it returns the
// final response and its body, already read under a size limit and closed; the
// caller must not read resp.Body again. When the budget is spent it returns
// ErrRateLimited. build runs once per attempt so each retry gets a fresh body.
func Do(
	ctx context.Context,
	d Doer,
	maxRetries int,
	retryable func(*http.Response) bool,
	build func() (*http.Request, error),
) (*http.Response, []byte, error) {
	if retryable == nil {
		retryable = func(resp *http.Response) bool {
			return resp.StatusCode == http.StatusTooManyRequests
		}
	}
	for attempt := 0; ; attempt++ {
		req, err := build()
		if err != nil {
			return nil, nil, err
		}
		resp, err := d.Do(req)
		if err != nil {
			return nil, nil, err
		}
		if retryable(resp) {
			wait, ok := RetryAfter(resp)
			if !ok {
				wait = defaultBackoff
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxResponseBytes))
			_ = resp.Body.Close()
			if attempt >= maxRetries {
				return nil, nil, ErrRateLimited
			}
			if err := Sleep(ctx, wait); err != nil {
				return nil, nil, err
			}
			continue
		}
		body, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
		_ = resp.Body.Close()
		if err != nil {
			return resp, nil, fmt.Errorf("read body: %w", err)
		}
		return resp, body, nil
	}
}

// RetryAfter reports how long to wait before retrying when resp carries a
// Retry-After header. It accepts both the integer-seconds form and the
// HTTP-date form; any other value reports no wait.
func RetryAfter(resp *http.Response) (time.Duration, bool) {
	v := strings.TrimSpace(resp.Header.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		if d := time.Until(t); d > 0 {
			return d, true
		}
		return 0, true
	}
	return 0, false
}

// Sleep waits for d or until ctx is canceled.
func Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Get returns a request builder for a GET carrying the given headers, as pairs
// of name then value. Every API client wrote the same seven lines to build one,
// differing only in which credential and which media type they sent, so the one
// place a request is actually made was the one place hardest to change.
//
// An odd trailing name with no value is ignored rather than panicking: a header
// missing from an outgoing request is a far smaller problem than a crash in a
// connector midway through a long read.
func Get(ctx context.Context, url string, headers ...string) func() (*http.Request, error) {
	return func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return nil, fmt.Errorf("new request: %w", err)
		}
		for i := 0; i+1 < len(headers); i += 2 {
			if headers[i] != "" && headers[i+1] != "" {
				req.Header.Set(headers[i], headers[i+1])
			}
		}
		return req, nil
	}
}
