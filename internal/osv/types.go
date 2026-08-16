// Package osv implements the two deterministic dep-risk data sources: the
// OSV vulnerability database and deps.dev's release-age lookup. Both
// clients degrade instead of failing the run — internal/handlers/deprisk.go
// turns a client error into a single warning finding, never a run failure.
package osv

// QueryBatchRequest is POST /v1/querybatch's body.
type QueryBatchRequest struct {
	Queries []Query `json:"queries"`
}

// Query is one package+version to check.
type Query struct {
	Package PackageQuery `json:"package"`
	Version string       `json:"version"`
}

// PackageQuery names a package within an OSV ecosystem.
type PackageQuery struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
}

// QueryBatchResponse is /v1/querybatch's response: matching vulnerability
// IDs only, in the same order as the request's Queries.
type QueryBatchResponse struct {
	Results []BatchResult `json:"results"`
}

// BatchResult is one query's matching vulnerability IDs.
type BatchResult struct {
	Vulns []VulnRef `json:"vulns"`
}

// VulnRef is one batch-result vulnerability reference; the full record is
// fetched separately via GET /v1/vulns/{id}.
type VulnRef struct {
	ID string `json:"id"`
}

// Vuln is a GET /v1/vulns/{id} response — only the fields dep-risk needs.
type Vuln struct {
	ID               string           `json:"id"`
	Summary          string           `json:"summary"`
	Severity         []Severity       `json:"severity"`
	DatabaseSpecific DatabaseSpecific `json:"database_specific"`
}

// Severity is one CVSS (or other) severity entry.
type Severity struct {
	Type  string `json:"type"`  // "CVSS_V3" | "CVSS_V4" | ...
	Score string `json:"score"` // a CVSS vector string
}

// DatabaseSpecific is the aggregator-added metadata dep-risk reads;
// unspecified fields are ignored.
type DatabaseSpecific struct {
	Severity string `json:"severity"` // e.g. "CRITICAL", "HIGH", "MODERATE", "LOW"
}

// VersionInfo is deps.dev's GET .../versions/{version} response — only the
// field dep-risk needs.
type VersionInfo struct {
	PublishedAt string `json:"publishedAt"` // RFC 3339; "" if unavailable
}
