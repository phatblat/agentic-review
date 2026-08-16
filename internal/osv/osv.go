package osv

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/phatblat/agentic-review/internal/logx"
)

const clientTimeout = 20 * time.Second

// Client looks up known advisories for a dependency's new version.
type Client struct {
	http    *http.Client
	baseURL string
}

// NewClient builds an OSV client against api.osv.dev.
func NewClient() *Client {
	return NewClientWithBaseURL("https://api.osv.dev")
}

// NewClientWithBaseURL builds an OSV client against a custom base URL —
// primarily so dependent packages' tests can point it at an httptest
// server.
func NewClientWithBaseURL(baseURL string) *Client {
	return &Client{http: &http.Client{Timeout: clientTimeout}, baseURL: baseURL}
}

// osvEcosystem maps agentic-review's manifest ecosystem names to OSV
// ecosystem names. deno resolves to npm: deno specifiers can name either an
// npm or a JSR package, but facts.DepChange does not preserve which — JSR
// support is out of v1 scope, so a JSR-only package simply returns no
// matches here rather than being actively skipped.
func osvEcosystem(ecosystem string) (string, bool) {
	switch ecosystem {
	case "cargo":
		return "crates.io", true
	case "npm", "deno":
		return "npm", true
	case "go":
		return "Go", true
	default:
		return "", false
	}
}

// Lookup returns every known advisory affecting name@version in ecosystem
// (agentic-review's own ecosystem name — cargo, npm, go, or deno). An
// unmapped ecosystem returns (nil, nil): no lookup attempted, not an
// error.
func (c *Client) Lookup(ctx context.Context, ecosystem, name, version string) ([]Vuln, error) {
	osvEco, ok := osvEcosystem(ecosystem)
	if !ok {
		logx.Debug("osv: no OSV ecosystem mapping for %q; skipping lookup for %s", ecosystem, name)
		return nil, nil
	}

	ids, err := c.queryBatch(ctx, osvEco, name, version)
	if err != nil {
		return nil, err
	}
	if len(ids) == 0 {
		return nil, nil
	}

	vulns := make([]Vuln, 0, len(ids))
	for _, id := range ids {
		v, err := c.getVuln(ctx, id)
		if err != nil {
			return nil, err
		}
		vulns = append(vulns, *v)
	}
	return vulns, nil
}

func (c *Client) queryBatch(ctx context.Context, ecosystem, name, version string) ([]string, error) {
	body, err := json.Marshal(QueryBatchRequest{Queries: []Query{{
		Package: PackageQuery{Ecosystem: ecosystem, Name: name},
		Version: version,
	}}})
	if err != nil {
		return nil, fmt.Errorf("osv: marshal querybatch request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/querybatch", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("osv: build querybatch request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osv: querybatch: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("osv: querybatch: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv: querybatch: %d: %s", resp.StatusCode, respBody)
	}

	var out QueryBatchResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return nil, fmt.Errorf("osv: querybatch: decode response: %w", err)
	}
	if len(out.Results) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(out.Results[0].Vulns))
	for _, v := range out.Results[0].Vulns {
		ids = append(ids, v.ID)
	}
	return ids, nil
}

func (c *Client) getVuln(ctx context.Context, id string) (*Vuln, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/v1/vulns/"+id, nil)
	if err != nil {
		return nil, fmt.Errorf("osv: build vuln request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("osv: get vuln %s: %w", id, err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("osv: get vuln %s: read response: %w", id, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("osv: get vuln %s: %d: %s", id, resp.StatusCode, respBody)
	}

	var v Vuln
	if err := json.Unmarshal(respBody, &v); err != nil {
		return nil, fmt.Errorf("osv: get vuln %s: decode response: %w", id, err)
	}
	return &v, nil
}
