package osv

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDepsDevPublishedAt(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/systems/CARGO/packages/openssl/versions/3.2.1", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte(`{"publishedAt": "2024-01-15T00:00:00Z"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &DepsDevClient{http: srv.Client(), baseURL: srv.URL}
	got, err := c.PublishedAt(context.Background(), "cargo", "openssl", "3.2.1")
	if err != nil {
		t.Fatalf("PublishedAt: %v", err)
	}
	if got != "2024-01-15T00:00:00Z" {
		t.Errorf("PublishedAt = %q", got)
	}
	if gotPath != "/v3/systems/CARGO/packages/openssl/versions/3.2.1" {
		t.Errorf("path = %q", gotPath)
	}
}

func TestDepsDevScopedPackageEscaped(t *testing.T) {
	var gotPath string
	mux := http.NewServeMux()
	mux.HandleFunc("/v3/systems/NPM/packages/%40scope%2Fname/versions/1.0.0", func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"publishedAt": "2024-01-15T00:00:00Z"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &DepsDevClient{http: srv.Client(), baseURL: srv.URL}
	if _, err := c.PublishedAt(context.Background(), "npm", "@scope/name", "1.0.0"); err != nil {
		t.Fatalf("PublishedAt: %v", err)
	}
	if gotPath == "" {
		t.Fatalf("handler never received a request; scoped-name escaping likely broke routing")
	}
}

func TestDepsDevNotFoundReturnsEmpty(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &DepsDevClient{http: srv.Client(), baseURL: srv.URL}
	got, err := c.PublishedAt(context.Background(), "npm", "missing-pkg", "1.0.0")
	if err != nil || got != "" {
		t.Errorf("PublishedAt = (%q, %v), want (\"\", nil) on 404", got, err)
	}
}

func TestDepsDevUnmappedEcosystem(t *testing.T) {
	c := NewDepsDevClient()
	got, err := c.PublishedAt(context.Background(), "deno", "x", "1.0.0")
	if err != nil || got != "" {
		t.Errorf("PublishedAt(deno) = (%q, %v), want (\"\", nil) with no HTTP call", got, err)
	}
}
