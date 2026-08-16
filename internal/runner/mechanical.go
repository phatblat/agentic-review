package runner

import (
	"context"
	"sort"
	"strings"

	"github.com/phatblat/agentic-review/internal/diffscan"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// mechanicalValidate runs every mechanical check (spec §6.2) on one
// persona's freshly-stamped findings before any verifier model runs:
// evidence byte-match, anchor validity, suggested_fix dry-run, and
// max_findings truncation. It returns every finding, including dropped
// ones — dropped findings carry Envelope.Verification.Disposition ==
// dropped and belong in findings.raw.json/verdicts.json; callers building
// findings.final.json filter by disposition == accepted.
//
// Agent-kind findings with no evidence never reach here: findings.v1's
// schema requires evidence with minItems: 1, so schema.DecodeFindings
// already rejected them.
func mechanicalValidate(ctx context.Context, findings []schema.Finding, store *gh.ContentStore, coverage map[string]diffscan.Coverage, diffPaths map[string]bool, headSHA, baseSHA string, maxFindings int) []schema.Finding {
	survivors := make([]schema.Finding, 0, len(findings))
	dropped := make([]schema.Finding, 0)

	for _, f := range findings {
		if f.Envelope.PersonaKind == string(persona.KindAgent) && !evidenceByteMatches(ctx, f, store, headSHA, baseSHA) {
			f.Envelope.Verification.Disposition = schema.DispositionDropped
			f.Envelope.Verification.Verdicts = append(f.Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
				Lens: "groundedness", Result: "fail", Checked: "mechanical",
			})
			dropped = append(dropped, f)
			continue
		}
		f = validateAnchor(f, coverage, diffPaths)
		f = dryRunSuggestedFix(ctx, f, store, headSHA)
		survivors = append(survivors, f)
	}

	survivors = truncateFindings(survivors, maxFindings)
	return append(survivors, dropped...)
}

// evidenceByteMatches reports whether every evidence entry byte-matches
// the file content at the appropriate ref (head, or base for a
// ref:base/deleted-code anchor), after trailing-whitespace normalisation
// per line.
func evidenceByteMatches(ctx context.Context, f schema.Finding, store *gh.ContentStore, headSHA, baseSHA string) bool {
	ref := headSHA
	if f.Payload.Anchor.Ref == schema.RefBase {
		ref = baseSHA
	}
	for _, e := range f.Payload.Evidence {
		content, err := store.Get(ctx, e.Path, ref)
		if err != nil {
			return false
		}
		lines := strings.Split(string(content), "\n")
		if e.StartLine < 1 || e.EndLine < e.StartLine || e.EndLine > len(lines) {
			return false
		}
		want := normalizeTrailingWhitespace(strings.Join(lines[e.StartLine-1:e.EndLine], "\n"))
		got := normalizeTrailingWhitespace(e.Source)
		if want != got {
			return false
		}
	}
	return true
}

func normalizeTrailingWhitespace(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t\r")
	}
	return strings.Join(lines, "\n")
}

// validateAnchor demotes a line anchor whose lines are not all covered by
// the diff (Right for ref:head, Left for ref:base): to file when the path
// is in the diff, else to pr.
func validateAnchor(f schema.Finding, coverage map[string]diffscan.Coverage, diffPaths map[string]bool) schema.Finding {
	if f.Payload.Anchor.Kind != schema.AnchorLine {
		return f
	}

	cov, ok := coverage[f.Payload.Anchor.Path]
	valid := ok
	if ok {
		lines := cov.Right
		if f.Payload.Anchor.Ref == schema.RefBase {
			lines = cov.Left
		}
		for line := f.Payload.Anchor.StartLine; line <= f.Payload.Anchor.EndLine; line++ {
			if !lines[line] {
				valid = false
				break
			}
		}
	}
	if valid {
		return f
	}

	logx.Warn("runner: %s: anchor %s:%d-%d falls outside changed hunks; demoting",
		f.Envelope.Persona, f.Payload.Anchor.Path, f.Payload.Anchor.StartLine, f.Payload.Anchor.EndLine)
	if diffPaths[f.Payload.Anchor.Path] {
		f.Payload.Anchor.Kind = schema.AnchorFile
	} else {
		f.Payload.Anchor.Kind = schema.AnchorPR
	}
	f.Payload.Anchor.StartLine = 0
	f.Payload.Anchor.EndLine = 0
	return f
}

// dryRunSuggestedFix drops a suggested fix that cannot be applied cleanly
// to head content, keeping the finding itself. Fork-PR rendering (plain
// fence vs. a committable suggestion block) is internal/render's concern,
// not mechanical validation — a valid fix is kept as-is here regardless of
// fork status.
func dryRunSuggestedFix(ctx context.Context, f schema.Finding, store *gh.ContentStore, headSHA string) schema.Finding {
	sf := f.Payload.SuggestedFix
	if sf == nil {
		return f
	}

	content, err := store.Get(ctx, f.Payload.Anchor.Path, headSHA)
	if err != nil {
		logx.Debug("runner: %s: suggested_fix dry-run: read %s: %v", f.Envelope.Persona, f.Payload.Anchor.Path, err)
		f.Payload.SuggestedFix = nil
		return f
	}
	lines := strings.Split(string(content), "\n")
	if sf.StartLine < 1 || sf.EndLine < sf.StartLine || sf.EndLine > len(lines) {
		logx.Debug("runner: %s: suggested_fix dry-run: range %d-%d out of bounds (%d lines)",
			f.Envelope.Persona, sf.StartLine, sf.EndLine, len(lines))
		f.Payload.SuggestedFix = nil
		return f
	}
	return f
}

// truncateFindings drops findings beyond maxFindings, lowest-severity
// first; blockers are exempt and never dropped for truncation.
func truncateFindings(findings []schema.Finding, maxFindings int) []schema.Finding {
	if maxFindings <= 0 || len(findings) <= maxFindings {
		return findings
	}

	order := make([]int, len(findings))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		return severityRank(findings[order[i]].Payload.Severity) < severityRank(findings[order[j]].Payload.Severity)
	})

	overage := len(findings) - maxFindings
	drop := make(map[int]bool, overage)
	for _, idx := range order {
		if overage <= 0 {
			break
		}
		if findings[idx].Payload.Severity == "blocker" {
			continue
		}
		drop[idx] = true
		overage--
	}

	out := make([]schema.Finding, len(findings))
	copy(out, findings)
	for idx := range drop {
		out[idx].Envelope.Verification.Disposition = schema.DispositionDropped
	}
	return out
}

func severityRank(s string) int {
	for i, sev := range schema.Severities {
		if sev == s {
			return i
		}
	}
	return -1
}
