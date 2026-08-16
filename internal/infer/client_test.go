package infer

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCompletePinsDeterminismFields(t *testing.T) {
	var got Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Fatalf("decode request: %v", err)
		}
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":2,"total_tokens":3}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("")
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

func TestCompleteRetriesOn5xx(t *testing.T) {
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

	c := NewHTTPClient("")
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

func TestCompleteDoesNotRetryOn4xx(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte("bad request"))
	}))
	defer srv.Close()

	c := NewHTTPClient("")
	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err == nil {
		t.Fatalf("Complete succeeded, want an error")
	}
	if attempts != 1 {
		t.Errorf("attempts = %d, want 1 (no retry on 4xx)", attempts)
	}
}

func TestCompleteFailsAfterOneRetry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := NewHTTPClient("")
	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err == nil {
		t.Fatalf("Complete succeeded, want an error after exhausting the retry")
	}
	if attempts != 2 {
		t.Errorf("attempts = %d, want 2", attempts)
	}
}

func TestCompleteSendsAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant"}}],"usage":{}}`))
	}))
	defer srv.Close()

	c := NewHTTPClient("secret-key")
	if _, err := c.Complete(context.Background(), srv.URL, &Request{Model: "m"}); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotAuth != "Bearer secret-key" {
		t.Errorf("Authorization = %q, want \"Bearer secret-key\"", gotAuth)
	}
}
