package infer

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Client completes one chat-completions turn against endpoint.
type Client interface {
	Complete(ctx context.Context, endpoint string, req *Request) (*Response, error)
}

const requestTimeout = 300 * time.Second

// retryableError marks an error eligible for the single network/5xx retry;
// 4xx responses are never retried.
type retryableError struct{ err error }

func (r *retryableError) Error() string { return r.err.Error() }
func (r *retryableError) Unwrap() error { return r.err }

// HTTPClient is the real OpenAI-compatible Client.
type HTTPClient struct {
	http   *http.Client
	apiKey string
}

// NewHTTPClient builds an HTTPClient authenticated with apiKey (may be
// empty for an unauthenticated endpoint).
func NewHTTPClient(apiKey string) *HTTPClient {
	return &HTTPClient{http: &http.Client{Timeout: requestTimeout}, apiKey: apiKey}
}

var _ Client = (*HTTPClient)(nil)

// Complete posts to endpoint + "/chat/completions". Temperature, TopP, and
// Seed are pinned to 0, 1, 1 on every call so recordings replay
// meaningfully. One retry on a network error or 5xx; none on 4xx.
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

	resp, err := c.doOnce(ctx, url, body)
	if err == nil {
		return resp, nil
	}
	var retryable *retryableError
	if !errors.As(err, &retryable) {
		return nil, err
	}
	return c.doOnce(ctx, url, body)
}

func (c *HTTPClient) doOnce(ctx context.Context, url string, body []byte) (*Response, error) {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("infer: build request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := c.http.Do(httpReq)
	if err != nil {
		return nil, &retryableError{fmt.Errorf("infer: %s: %w", url, err)}
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, &retryableError{fmt.Errorf("infer: %s: read response: %w", url, err)}
	}

	switch {
	case resp.StatusCode >= 500:
		return nil, &retryableError{fmt.Errorf("infer: %s: %d: %s", url, resp.StatusCode, respBody)}
	case resp.StatusCode >= 400:
		return nil, fmt.Errorf("infer: %s: %d: %s", url, resp.StatusCode, respBody)
	}

	var out Response
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("infer: %s: decode response: %w", url, err)
	}
	return &out, nil
}
