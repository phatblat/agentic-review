package infer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// withZeroBackoffs replaces the package's fixed retry backoff with zero
// durations for the duration of t, restoring the original values on
// cleanup — keeps exhausted-retry tests fast and deterministic without
// changing production's fixed 250ms/1s/4s values.
func withZeroBackoffs(t *testing.T) {
	t.Helper()
	orig := retryBackoffs
	retryBackoffs = [3]time.Duration{0, 0, 0}
	t.Cleanup(func() { retryBackoffs = orig })
}

func TestCompletePinsDeterminismFields(t *testing.T) {
	var got Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	req := &Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Temperature: 0.9, TopP: 0.5, Seed: 42}
	resp, err := c.Complete(context.Background(), srv.URL, req)
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if got.Temperature != 0 || got.TopP != 1 || got.Seed != 1 {
		t.Errorf("sent request = %+v, want Temperature=0 TopP=1 Seed=1", got)
	}
	if resp.Usage.TotalTokens != 3 || resp.Choices[0].Message.Content != "ok" {
		t.Errorf("resp = %+v", resp)
	}
	// The caller's own Request value must not be mutated by Complete.
	if req.Temperature != 0.9 {
		t.Errorf("caller's Request.Temperature was mutated to %v", req.Temperature)
	}
}

// TestCompleteSendsOnlyResponseFormat is the regression for Casper's 400
// response: the request body must be strictly OpenAI-compatible, with no
// vLLM guided_json extra sent alongside response_format.
func TestCompleteSendsOnlyResponseFormat(t *testing.T) {
	var got map[string]json.RawMessage
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	req := &Request{
		Model:    "m",
		Messages: []Message{{Role: "user", Content: "hi"}},
		ResponseFormat: &ResponseFormat{
			Type:       "json_schema",
			JSONSchema: JSONSchemaSpec{Name: "findings_v1", Schema: json.RawMessage(`{"type":"object"}`), Strict: true},
		},
	}
	if _, err := c.Complete(context.Background(), srv.URL, req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if _, ok := got["guided_json"]; ok {
		t.Errorf("request body included guided_json, want the OpenAI-compatible response_format only: %s", got["guided_json"])
	}
	rf, ok := got["response_format"]
	if !ok {
		t.Fatalf("request body missing response_format: %v", got)
	}
	var decoded struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(rf, &decoded); err != nil {
		t.Fatalf("decode response_format: %v", err)
	}
	if decoded.Type != "json_schema" {
		t.Errorf("response_format.type = %q, want %q", decoded.Type, "json_schema")
	}
}

func TestCompleteRetriesOn5xx(t *testing.T) {
	withZeroBackoffs(t)
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("boom"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	resp, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one retry)", attempts)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("resp = %+v", resp)
	}
}

// TestCompleteRetriesOn429 is the regression for Casper's rate limiting:
// a 429 with a Retry-After header must retry rather than fail outright.
func TestCompleteRetriesOn429(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte("rate limited"))
			return
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	resp, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2 (one 429 retry)", attempts)
	}
	if resp.Choices[0].Message.Content != "ok" {
		t.Errorf("resp = %+v", resp)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		value  string
		want   time.Duration
		wantOK bool
	}{
		{"seconds", "5", 5 * time.Second, true},
		{"http date", now.Add(10 * time.Second).Format(http.TimeFormat), 10 * time.Second, true},
		{"past date", now.Add(-10 * time.Second).Format(http.TimeFormat), 0, true},
		{"malformed", "not-a-delay", 0, false},
		{"missing", "", 0, false},
		{"exceeds 60s cap", "3600", 60 * time.Second, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseRetryAfter(tc.value, now)
			if ok != tc.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tc.wantOK)
			}
			if ok && got != tc.want {
				t.Errorf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCompleteDoesNotRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err == nil {
		t.Fatalf("Complete succeeded, want an error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestCompleteFailsAfterRetries(t *testing.T) {
	withZeroBackoffs(t)
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err == nil {
		t.Fatalf("Complete succeeded, want an error after exhausting every retry")
	}
	if attempts != 4 {
		t.Errorf("attempts = %d, want 4 (one initial request plus three retries)", attempts)
	}
}

// TestCompleteStopsRetryingWhenContextCanceled cancels ctx while a
// nonzero retry delay is pending and asserts no subsequent request is
// sent — the sleep is aborted rather than waited out.
func TestCompleteStopsRetryingWhenContextCanceled(t *testing.T) {
	attempts := 0
	ctx, cancel := context.WithCancel(context.Background())
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
		cancel() // cancel while this response's fixed 250ms retry delay would be pending
	}))
	defer srv.Close()

	c := NewHTTPClient("", nil)
	_, err := c.Complete(ctx, srv.URL, &Request{Model: "m"})
	if err == nil {
		t.Fatalf("Complete succeeded, want a canceled-retry error")
	}
	if !strings.Contains(err.Error(), "retry canceled") {
		t.Errorf("err = %v, want it to mention a canceled retry", err)
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (canceled context aborts the pending retry sleep)", attempts)
	}
}

func TestCompleteSendsConfiguredHeaders(t *testing.T) {
	var gotAuth, gotSession, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotSession = r.Header.Get("x-session-id")
		gotContentType = r.Header.Get("Content-Type")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant"}}],"usage":{}}`))
	}))
	defer srv.Close()

	headers := http.Header{"X-Session-Id": []string{"run-42"}}
	c := NewHTTPClient("secret-key", headers)
	headers.Set("X-Session-Id", "mutated-after-construction") // must not affect the client

	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want \"Bearer secret-key\"", gotAuth)
	}
	if gotSession != "run-42" {
		t.Errorf("x-session-id = %q, want %q (the client's cloned header, unaffected by the caller's later mutation)", gotSession, "run-42")
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type = %q, want \"application/json\"", gotContentType)
	}
}

// TestMeterAggregatesUsageAcrossCalls proves Meter both preserves the
// nested cached-token detail on every decoded response and sums usage
// correctly across multiple calls.
func TestMeterAggregatesUsageAcrossCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}],` +
			`"usage":{"prompt_tokens":10,"prompt_tokens_details":{"cached_tokens":4},"completion_tokens":2,"total_tokens":12}}`))
	}))
	defer srv.Close()

	m := NewMeter(NewHTTPClient("", nil))
	for range 2 {
		resp, err := m.Complete(context.Background(), srv.URL, &Request{Model: "m"})
		if err != nil {
			t.Fatalf("Complete: %v", err)
		}
		if resp.Usage.PromptTokensDetails.CachedTokens != 4 {
			t.Errorf("resp.Usage.PromptTokensDetails.CachedTokens = %d, want 4", resp.Usage.PromptTokensDetails.CachedTokens)
		}
	}
	got := m.Usage()
	want := Usage{PromptTokens: 20, PromptTokensDetails: PromptTokensDetails{CachedTokens: 8}, CompletionTokens: 4, TotalTokens: 24}
	if got != want {
		t.Errorf("Usage() = %+v, want %+v", got, want)
	}
}
