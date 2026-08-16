package handlers

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/osv"
)

func testClients(t *testing.T, osvHandler, depsDevHandler http.HandlerFunc) (*osv.Client, *osv.DepsDevClient) {
	t.Helper()
	osvMux := http.NewServeMux()
	if osvHandler != nil {
		osvMux.HandleFunc("/", osvHandler)
	} else {
		osvMux.HandleFunc("/v1/querybatch", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"results":[{}]}`))
		})
	}
	osvSrv := httptest.NewServer(osvMux)
	t.Cleanup(osvSrv.Close)

	depsMux := http.NewServeMux()
	if depsDevHandler != nil {
		depsMux.HandleFunc("/", depsDevHandler)
	} else {
		depsMux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"publishedAt": "2020-01-01T00:00:00Z"}`))
		})
	}
	depsSrv := httptest.NewServer(depsMux)
	t.Cleanup(depsSrv.Close)

	return osv.NewClientWithBaseURL(osvSrv.URL), osv.NewDepsDevClientWithBaseURL(depsSrv.URL)
}

func TestDepRiskCriticalCVSSIsBlocker(t *testing.T) {
	osvHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"OSV-1"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{
			"id": "OSV-1", "summary": "critical RCE",
			"severity": [{"type":"CVSS_V3","score":"CVSS:3.1/AV:N/AC:L/PR:N/UI:N/S:U/C:H/I:H/A:H"}]
		}`))
	}
	osvClient, depsDevClient := testClients(t, osvHandler, nil)

	changed := []facts.DepChange{{Ecosystem: "cargo", Name: "openssl", From: "1.0.2", To: "3.2.1", Bump: "major", Path: "Cargo.toml"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)

	var blockers, warnings int
	for _, p := range out {
		if p.Severity == "blocker" {
			blockers++
			if p.Anchor.Kind != "file" || p.Anchor.Path != "Cargo.toml" {
				t.Errorf("blocker anchor = %+v, want file Cargo.toml", p.Anchor)
			}
			if p.Category != "security" || len(p.Domains) != 1 || p.Domains[0] != "dependencies" {
				t.Errorf("blocker category/domains = %s/%v", p.Category, p.Domains)
			}
		}
		if p.Severity == "warning" {
			warnings++ // the major-bump warning
		}
	}
	if blockers != 1 {
		t.Errorf("blockers = %d, want 1", blockers)
	}
	if warnings != 1 {
		t.Errorf("warnings = %d, want 1 (major bump)", warnings)
	}
}

func TestDepRiskDatabaseSpecificCriticalIsBlocker(t *testing.T) {
	osvHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"OSV-2"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"OSV-2","summary":"x","database_specific":{"severity":"CRITICAL"}}`))
	}
	osvClient, depsDevClient := testClients(t, osvHandler, nil)

	changed := []facts.DepChange{{Ecosystem: "npm", Name: "lodash", From: "4.17.20", To: "4.17.21", Bump: "patch", Path: "package.json"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)

	found := false
	for _, p := range out {
		if p.Severity == "blocker" {
			found = true
		}
	}
	if !found {
		t.Errorf("out = %+v, want a blocker from database_specific.severity=CRITICAL", out)
	}
}

func TestDepRiskNonCriticalAdvisoryIsError(t *testing.T) {
	osvHandler := func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/querybatch" {
			_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"OSV-3"}]}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"OSV-3","summary":"minor issue","severity":[{"type":"CVSS_V3","score":"CVSS:3.1/AV:L/AC:H/PR:H/UI:R/S:U/C:L/I:N/A:N"}]}`))
	}
	osvClient, depsDevClient := testClients(t, osvHandler, nil)

	changed := []facts.DepChange{{Ecosystem: "npm", Name: "x", From: "1.0.0", To: "1.0.1", Bump: "patch", Path: "package.json"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)

	found := false
	for _, p := range out {
		if p.Severity == "error" {
			found = true
		}
		if p.Severity == "blocker" {
			t.Errorf("low-severity advisory produced a blocker: %+v", p)
		}
	}
	if !found {
		t.Errorf("out = %+v, want an error-severity finding", out)
	}
}

func TestDepRiskFreshReleaseWarning(t *testing.T) {
	fresh := time.Now().Add(-3 * 24 * time.Hour).Format(time.RFC3339)
	depsDevHandler := func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"publishedAt": "` + fresh + `"}`))
	}
	osvClient, depsDevClient := testClients(t, nil, depsDevHandler)

	changed := []facts.DepChange{{Ecosystem: "npm", Name: "x", From: "1.0.0", To: "1.0.1", Bump: "patch", Path: "package.json"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)

	found := false
	for _, p := range out {
		if p.Severity == "warning" {
			found = true
		}
	}
	if !found {
		t.Errorf("out = %+v, want a fresh-release warning", out)
	}
}

func TestDepRiskOldReleaseNoWarning(t *testing.T) {
	osvClient, depsDevClient := testClients(t, nil, nil) // default handler returns a 2020 date
	changed := []facts.DepChange{{Ecosystem: "npm", Name: "x", From: "1.0.0", To: "1.0.1", Bump: "patch", Path: "package.json"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)
	if len(out) != 0 {
		t.Errorf("out = %+v, want no findings for a clean, old, patch-level bump", out)
	}
}

func TestDepRiskOSVFailureDegrades(t *testing.T) {
	osvHandler := func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}
	osvClient, depsDevClient := testClients(t, osvHandler, nil)
	changed := []facts.DepChange{{Ecosystem: "npm", Name: "x", From: "1.0.0", To: "1.0.1", Bump: "patch", Path: "package.json"}}
	out := DepRisk(context.Background(), osvClient, depsDevClient, changed)

	found := false
	for _, p := range out {
		if p.Anchor.Kind == "pr" && p.Claim == "dependency risk data unavailable (api.osv.dev unreachable)" {
			found = true
		}
	}
	if !found {
		t.Errorf("out = %+v, want the degraded-data warning anchored at pr", out)
	}
}
