// Package handlers implements the deterministic personas' runtime.handler
// bodies (spec §14): builtin/dep-risk and builtin/config-guard. Team
// execution (internal/runner) dispatches a deterministic-kind persona here
// instead of to a model.
package handlers

import (
	"context"
	"fmt"
	"time"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/osv"
	"github.com/phatblat/agentic-review/internal/schema"
)

// typosquatWindow is how recently a dependency's new version must have
// been published to earn the release-age warning (a typosquat or
// account-compromise signal).
const typosquatWindow = 7 * 24 * time.Hour

// DepRisk implements builtin/dep-risk. For every dependency change: a
// blocker when the new version has a known advisory with CVSS >= 9.0 or
// database_specific.severity == "CRITICAL"; an error for any other
// advisory on the new version; a warning for a major bump; a warning when
// the new version's publishedAt is under 7 days old.
//
// Both OSV clients degrade instead of failing the run: on any lookup
// error, DepRisk emits one warning finding anchored at pr and logs a
// warning, rather than aborting.
func DepRisk(ctx context.Context, osvClient *osv.Client, depsDevClient *osv.DepsDevClient, changed []facts.DepChange) []schema.Payload {
	var out []schema.Payload
	degraded := false

	for _, dc := range changed {
		vulns, err := osvClient.Lookup(ctx, dc.Ecosystem, dc.Name, dc.To)
		switch {
		case err != nil:
			logx.Warn("dep-risk: osv lookup for %s@%s: %v", dc.Name, dc.To, err)
			degraded = true
		default:
			out = append(out, adviseFindings(dc, vulns)...)
		}

		if dc.Bump == "major" {
			out = append(out, majorBumpFinding(dc))
		}

		publishedAt, err := depsDevClient.PublishedAt(ctx, dc.Ecosystem, dc.Name, dc.To)
		switch {
		case err != nil:
			logx.Warn("dep-risk: deps.dev lookup for %s@%s: %v", dc.Name, dc.To, err)
			degraded = true
		default:
			if young, ok := isYoungRelease(publishedAt); ok && young {
				out = append(out, freshReleaseFinding(dc, publishedAt))
			}
		}
	}

	if degraded {
		out = append(out, schema.Payload{
			Category:   "security",
			Severity:   "warning",
			Title:      "dependency risk data unavailable",
			Claim:      "dependency risk data unavailable (api.osv.dev unreachable)",
			Domains:    []string{"dependencies"},
			Anchor:     schema.Anchor{Kind: schema.AnchorPR},
			Confidence: 1,
		})
	}

	return out
}

func adviseFindings(dc facts.DepChange, vulns []osv.Vuln) []schema.Payload {
	out := make([]schema.Payload, 0, len(vulns))
	for _, v := range vulns {
		severity := "error"
		if isCriticalAdvisory(v) {
			severity = "blocker"
		}
		out = append(out, schema.Payload{
			Category:   "security",
			Severity:   severity,
			Title:      fmt.Sprintf("%s: known advisory affecting %s %s", v.ID, dc.Name, dc.To),
			Claim:      adviseClaim(v, dc),
			Domains:    []string{"dependencies"},
			Anchor:     schema.Anchor{Kind: schema.AnchorFile, Path: dc.Path},
			Confidence: 1,
		})
	}
	return out
}

func isCriticalAdvisory(v osv.Vuln) bool {
	if v.DatabaseSpecific.Severity == "CRITICAL" {
		return true
	}
	for _, sev := range v.Severity {
		if sev.Type != "CVSS_V3" {
			continue
		}
		if score, ok := osv.BaseScoreV3(sev.Score); ok && score >= 9.0 {
			return true
		}
	}
	return false
}

func adviseClaim(v osv.Vuln, dc facts.DepChange) string {
	if v.Summary != "" {
		return fmt.Sprintf("%s: %s", v.ID, v.Summary)
	}
	return fmt.Sprintf("%s affects %s@%s with no published summary", v.ID, dc.Name, dc.To)
}

func majorBumpFinding(dc facts.DepChange) schema.Payload {
	return schema.Payload{
		Category:   "security",
		Severity:   "warning",
		Title:      fmt.Sprintf("%s: major version bump %s -> %s", dc.Name, dc.From, dc.To),
		Claim:      fmt.Sprintf("%s changed from %s to %s, a major version bump that may carry breaking or security-relevant changes not covered by an advisory", dc.Name, dc.From, dc.To),
		Domains:    []string{"dependencies"},
		Anchor:     schema.Anchor{Kind: schema.AnchorFile, Path: dc.Path},
		Confidence: 1,
	}
}

func freshReleaseFinding(dc facts.DepChange, publishedAt string) schema.Payload {
	return schema.Payload{
		Category:   "security",
		Severity:   "warning",
		Title:      fmt.Sprintf("%s@%s published within the last 7 days", dc.Name, dc.To),
		Claim:      fmt.Sprintf("%s@%s was published at %s, under 7 days ago — a typosquat or account-compromise window", dc.Name, dc.To, publishedAt),
		Domains:    []string{"dependencies"},
		Anchor:     schema.Anchor{Kind: schema.AnchorFile, Path: dc.Path},
		Confidence: 1,
	}
}

// isYoungRelease parses publishedAt (RFC 3339) and reports whether it is
// within typosquatWindow of now. ok is false when publishedAt is empty or
// unparseable, meaning deps.dev had no publish-date record.
func isYoungRelease(publishedAt string) (young, ok bool) {
	if publishedAt == "" {
		return false, false
	}
	t, err := time.Parse(time.RFC3339, publishedAt)
	if err != nil {
		return false, false
	}
	return time.Since(t) < typosquatWindow, true
}
