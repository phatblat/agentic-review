package osv

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/phatblat/agentic-review/internal/logx"
)

// DepsDevClient looks up a package version's publish date, the release-age
// input for dep-risk's typosquat/compromise-window heuristic.
type DepsDevClient struct {
	http    *http.Client
	baseURL string
}

// NewDepsDevClient builds a deps.dev client against api.deps.dev.
func NewDepsDevClient() *DepsDevClient {
	return NewDepsDevClientWithBaseURL("https://api.deps.dev")
}

// NewDepsDevClientWithBaseURL builds a deps.dev client against a custom
// base URL — primarily so dependent packages' tests can point it at an
// httptest server.
func NewDepsDevClientWithBaseURL(baseURL string) *DepsDevClient {
	return &DepsDevClient{http: &http.Client{Timeout: clientTimeout}, baseURL: baseURL}
}

// depsDevSystem maps agentic-review's manifest ecosystem names to deps.dev
// system names. deno is not supported: deps.dev has no npm/JSR-ambiguous
// "deno" system, and JSR itself is out of v1 scope.
func depsDevSystem(ecosystem string) (string, bool) {
	switch ecosystem {
	case "cargo":
		return "CARGO", true
	case "npm":
		return "NPM", true
	case "go":
		return "GO", true
	default:
		return "", false
	}
}

// PublishedAt returns name@version's publish time (RFC 3339), or "" if
// deps.dev has no record of it. An unmapped ecosystem returns ("", nil):
// no lookup attempted, not an error.
func (c *DepsDevClient) PublishedAt(ctx context.Context, ecosystem, name, version string) (string, error) {
	system, ok := depsDevSystem(ecosystem)
	if !ok {
		logx.Debug("depsdev: no deps.dev system mapping for %q; skipping lookup for %s", ecosystem, name)
		return "", nil
	}

	path := fmt.Sprintf("%s/v3/systems/%s/packages/%s/versions/%s",
		c.baseURL, system, url.PathEscape(name), url.PathEscape(version))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, path, nil)
	if err != nil {
		return "", fmt.Errorf("depsdev: build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return "", fmt.Errorf("depsdev: %s@%s: %w", name, version, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("depsdev: %s@%s: read response: %w", name, version, err)
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("depsdev: %s@%s: %d: %s", name, version, resp.StatusCode, body)
	}

	var out VersionInfo
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("depsdev: %s@%s: decode response: %w", name, version, err)
	}
	return out.PublishedAt, nil
}
