package osv

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientLookup(t *testing.T) {
	var gotQuery QueryBatchRequest
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/querybatch", func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotQuery); err != nil {
			t.Fatalf("decode querybatch: %v", err)
		}
		_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"OSV-2020-111"}]}]}`))
	})
	mux.HandleFunc("/v1/vulns/OSV-2020-111", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{
			"id": "OSV-2020-111",
			"summary": "critical bug",
			"severity": [{"type": "CVSS_V3", "score": "CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}],
			"database_specific": {"severity": "CRITICAL"}
		}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{http: srv.Client(), baseURL: srv.URL}
	vulns, err := c.Lookup(context.Background(), "cargo", "openssl", "1.0.2")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(vulns) != 1 || vulns[0].ID != "OSV-2020-111" {
		t.Fatalf("vulns = %+v", vulns)
	}
	if vulns[0].DatabaseSpecific.Severity != "CRITICAL" {
		t.Errorf("DatabaseSpecific.Severity = %q, want CRITICAL", vulns[0].DatabaseSpecific.Severity)
	}
	if gotQuery.Queries[0].Package.Ecosystem != "crates.io" || gotQuery.Queries[0].Package.Name != "openssl" {
		t.Errorf("gotQuery = %+v, want ecosystem=crates.io name=openssl", gotQuery)
	}
}

func TestClientLookupNoVulns(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/querybatch", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{}]}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{http: srv.Client(), baseURL: srv.URL}
	vulns, err := c.Lookup(context.Background(), "npm", "lodash", "4.17.21")
	if err != nil {
		t.Fatalf("Lookup: %v", err)
	}
	if len(vulns) != 0 {
		t.Errorf("vulns = %+v, want none", vulns)
	}
}

func TestClientLookupUnmappedEcosystem(t *testing.T) {
	c := NewClient()
	vulns, err := c.Lookup(context.Background(), "unknown-ecosystem", "x", "1.0.0")
	if err != nil || vulns != nil {
		t.Errorf("Lookup(unknown-ecosystem) = (%v, %v), want (nil, nil) with no HTTP call", vulns, err)
	}
}

func TestOsvEcosystemMapping(t *testing.T) {
	cases := map[string]string{"cargo": "crates.io", "npm": "npm", "go": "Go", "deno": "npm"}
	for eco, want := range cases {
		got, ok := osvEcosystem(eco)
		if !ok || got != want {
			t.Errorf("osvEcosystem(%q) = (%q, %v), want (%q, true)", eco, got, ok, want)
		}
	}
	if _, ok := osvEcosystem("ruby"); ok {
		t.Errorf("osvEcosystem(ruby) unexpectedly mapped")
	}
}

func TestClientLookupServerError(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/querybatch", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := &Client{http: srv.Client(), baseURL: srv.URL}
	if _, err := c.Lookup(context.Background(), "cargo", "x", "1.0.0"); err == nil {
		t.Fatalf("Lookup succeeded against a 500, want an error")
	}
}
