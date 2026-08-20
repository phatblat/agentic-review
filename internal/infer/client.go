package infer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Client completes one chat-completions turn against endpoint.
type Client interface {
	Complete(ctx context.Context, endpoint string, req *Request) (*Response, error)
}

const requestTimeout = 300 * time.Second

// retryBackoffs is the fixed 250ms/1s/4s fallback backoff for a retry
// whose response carried no usable server-advised delay — the same
// three-retry shape as internal/gh/retry.go. Package-scoped so tests can
// temporarily replace it with zero durations and restore it with
// t.Cleanup; production always uses these fixed values.
var retryBackoffs = [3]time.Duration{250 * time.Millisecond, time.Second, 4 * time.Second}

// maxRetrySleep caps both a server-advised Retry-After wait and the
// fixed fallback backoff (which never exceeds it in practice).
const maxRetrySleep = 60 * time.Second

// retryableError marks an error eligible for a bounded retry: a network
// error, a response-body read error, an HTTP 5xx, or an HTTP 429; every
// other 4xx response is immediately fatal. delay/hasDelay carry a 429's
// parsed Retry-After wait; when hasDelay is false the caller falls back
// to its own fixed backoff for the attempt.
type retryableError struct {
	err      error
	delay    time.Duration
	hasDelay bool
}

func (r *retryableError) Error() string { return r.err.Error() }
func (r *retryableError) Unwrap() error { return r.err }

// HTTPClient is the real OpenAI-compatible Client.
type HTTPClient struct {
	http    *http.Client
	apiKey  string
	headers http.Header
}

// NewHTTPClient builds an HTTPClient authenticated with apiKey (may be
// empty for an unauthenticated endpoint) and sending headers on every
// request alongside Content-Type and, when apiKey is set, Authorization
// (which always wins over any caller-supplied Authorization value).
// headers is cloned at construction so the caller's own map can't mutate
// the client afterward; a nil headers is treated as empty.
func NewHTTPClient(apiKey string, headers http.Header) *HTTPClient {
	cloned := headers.Clone()
	if cloned == nil {
		cloned = http.Header{}
	}
	return &HTTPClient{http: &http.Client{Timeout: requestTimeout}, apiKey: apiKey, headers: cloned}
}

var _ Client = (*HTTPClient)(nil)

// Complete posts to endpoint + "/chat/completions". Temperature, TopP, and
// Seed are pinned to 0, 1, 1 on every call so recordings replay
// meaningfully. Up to three retries follow a network error, a
// response-body read error, or an HTTP 5xx/429 response — four attempts
// total, matching internal/gh/retry.go's shape; every other 4xx is
// immediately fatal. A 429's Retry-After header sets that retry's delay
// (capped at 60s); otherwise the fixed 250ms/1s/4s fallback applies. A
// canceled ctx aborts a pending retry sleep instead of waiting it out.
func (c *HTTPClient) Complete(ctx context.Context, endpoint string, req *Request) (*Response, error) {
	pinned := *req
	pinned.Temperature = 0
	pinned.TopP = 1
	pinned.Seed = 1

	body, err := json.Marshal(pinned)
	if err != nil {
		return nil, fmt.Errorf("infer: marshal request: %w", err)
	}
	url := strings.TrimRight(endpoint, "/") + "/chat/completions"

	var lastErr error
	for attempt := 0; attempt <= len(retryBackoffs); attempt++ {
		resp, err := c.doOnce(ctx, url, body)
		if err == nil {
			return resp, nil
		}
		var retryable *retryableError
		if !errors.As(err, &retryable) {
			return nil, err
		}
		lastErr = retryable
		if attempt == len(retryBackoffs) {
			return nil, lastErr
		}
		delay := retryBackoffs[attempt]
		if retryable.hasDelay {
			delay = retryable.delay
		}
		if serr := sleepRetry(ctx, delay); serr != nil {
			return nil, serr
		}
	}
	return nil, lastErr
}

func (c *HTTPClient) doOnce(ctx context.Context, url string, body []byte) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("infer: build request: %w", err)
	}
	httpReq.Header = c.headers.Clone()
	if httpReq.Header == nil {
		httpReq.Header = http.Header{}
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, &retryableError{err: fmt.Errorf("infer: %s: %w", url, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &retryableError{err: fmt.Errorf("infer: %s: read response: %w", url, err)}
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		delay, ok := parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())
		return nil, &retryableError{
			err:      fmt.Errorf("infer: %s: %d: %s", url, resp.StatusCode, respBody),
			delay:    delay,
			hasDelay: ok,
		}
	case resp.StatusCode >= 500:
		return nil, &retryableError{err: fmt.Errorf("infer: %s: %d: %s", url, resp.StatusCode, respBody)}
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("infer: %s: %d: %s", url, resp.StatusCode, respBody)
	}

	var out Response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("infer: %s: decode response: %w", url, err)
	}
	return &out, nil
}

// parseRetryAfter parses an HTTP Retry-After header value: either an
// integer count of seconds or an HTTP-date. A past or negative delay
// clamps to zero; any delay is capped at maxRetrySleep. ok is false for
// an empty or unparseable value, in which case the caller falls back to
// its own fixed backoff for that attempt.
func parseRetryAfter(value string, now time.Time) (time.Duration, bool) {
	if value == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(strings.TrimSpace(value)); err == nil {
		return capDuration(time.Duration(secs) * time.Second), true
	}
	if when, err := http.ParseTime(value); err == nil {
		return capDuration(when.Sub(now)), true
	}
	return 0, false
}

func capDuration(d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	if d > maxRetrySleep {
		return maxRetrySleep
	}
	return d
}

// sleepRetry waits for delay using a context-aware timer, so a canceled
// ctx aborts a pending rate-limit sleep instead of waiting for delay to
// elapse.
func sleepRetry(ctx context.Context, delay time.Duration) error {
	if delay < 0 {
		delay = 0
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return fmt.Errorf("infer: retry canceled: %w", ctx.Err())
	}
}

// Meter is a Client decorator that accumulates prompt, cached-prompt,
// completion, and total token usage across every successful Complete
// call it observes. Safe for concurrent use, since one Meter observes
// every persona and lens call in a review run (spec §13.3's budget.json
// usage block).
type Meter struct {
	inner Client

	mu    sync.Mutex
	usage Usage
}

// NewMeter wraps inner, accumulating usage from every completion it
// observes.
func NewMeter(inner Client) *Meter {
	return &Meter{inner: inner}
}

var _ Client = (*Meter)(nil)

// Complete delegates to the wrapped Client and, on success, adds the
// response's usage into the running totals.
func (m *Meter) Complete(ctx context.Context, endpoint string, req *Request) (*Response, error) {
	resp, err := m.inner.Complete(ctx, endpoint, req)
	if err != nil {
		return nil, err
	}
	m.mu.Lock()
	m.usage.PromptTokens += resp.Usage.PromptTokens
	m.usage.PromptTokensDetails.CachedTokens += resp.Usage.PromptTokensDetails.CachedTokens
	m.usage.CompletionTokens += resp.Usage.CompletionTokens
	m.usage.TotalTokens += resp.Usage.TotalTokens
	m.mu.Unlock()
	return resp, nil
}

// Usage returns a snapshot of the totals accumulated so far; the
// returned value is a copy, so the caller cannot mutate the meter's
// internal state through it.
func (m *Meter) Usage() Usage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.usage
}
