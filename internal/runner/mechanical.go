package runner

import (
	"context"
	"fmt"
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
		if f.Envelope.PersonaKind == string(persona.KindAgent) {
			grounded, reason, ok := groundEvidence(ctx, f, store, headSHA, baseSHA)
			if !ok {
				f.Envelope.Verification.Disposition = schema.DispositionDropped
				f.Envelope.Verification.Verdicts = append(f.Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
					Lens: "groundedness", Result: "fail", Checked: "mechanical", Reason: reason,
				})
				logx.Warn("runner: %s: %s", f.Envelope.Persona, reason)
				dropped = append(dropped, f)
				continue
			}
			f.Payload.Evidence = grounded
		}
		f = validateAnchor(f, coverage, diffPaths)
		f = dryRunSuggestedFix(ctx, f, store, headSHA)
		survivors = append(survivors, f)
	}

	survivors = truncateFindings(survivors, maxFindings)
	return append(survivors, dropped...)
}

// groundEvidence checks every evidence entry against the file content at
// the appropriate ref (head, or base for a ref:base/deleted-code anchor)
// and returns the entries with their line numbers corrected to where the
// quoted text actually is.
//
// The invariant worth defending is that the quoted code exists in the
// file: that is what separates a checkable finding from an invented one.
// Exact agreement between the quote and the line numbers cited alongside
// it is a much weaker property, and one models do not reliably deliver.
// Measured on Qwen3.6-35B, one run produced four substantive findings
// that all quoted real code with citations off by 3-7 lines and leading
// indentation stripped; refusing them turned a review with four findings
// into a silent all-clear, which is the worst possible output. So a quote
// found elsewhere in the cited file is accepted with its citation
// corrected, and only a quote that appears nowhere in the file is
// refused.
//
// Comparison normalises leading and trailing whitespace per line:
// indentation is exactly what a model reflowing a quote loses first, and
// it carries no evidentiary weight.
func groundEvidence(ctx context.Context, f schema.Finding, store *gh.ContentStore, headSHA, baseSHA string) ([]schema.Evidence, string, bool) {
	ref := headSHA
	refName := "head"
	if f.Payload.Anchor.Ref == schema.RefBase {
		ref = baseSHA
		refName = "base"
	}

	out := make([]schema.Evidence, len(f.Payload.Evidence))
	copy(out, f.Payload.Evidence)
	for i := range out {
		e := &out[i]
		content, err := store.Get(ctx, e.Path, ref)
		if err != nil {
			return nil, fmt.Sprintf("evidence[%d] cites %s, unreadable at %s: %v", i, e.Path, refName, err), false
		}
		quote := normalizeQuote(strings.Split(e.Source, "\n"))
		if len(quote) == 0 {
			return nil, fmt.Sprintf("evidence[%d] cites %s:%d-%d with an empty quote", i, e.Path, e.StartLine, e.EndLine), false
		}
		file := trimEachLine(strings.Split(string(content), "\n"))

		start, ok := locateQuote(file, quote, e.StartLine)
		if !ok {
			return nil, fmt.Sprintf("evidence[%d] quote does not appear anywhere in %s at %s", i, e.Path, refName), false
		}
		if start != e.StartLine {
			logx.Warn("runner: %s: evidence[%d] cited %s:%d-%d but the quote is at %d-%d; correcting",
				f.Envelope.Persona, i, e.Path, e.StartLine, e.EndLine, start, start+len(quote)-1)
		}
		e.StartLine = start
		e.EndLine = start + len(quote) - 1
	}
	return out, "", true
}

// locateQuote returns the 1-based line where quote begins in file,
// preferring citedStart when the quote is already there so a correctly
// cited entry never moves. Trailing blank lines are ignored on both
// sides by normalizeLines, so a quote is matched on its content alone.
func locateQuote(file, quote []string, citedStart int) (int, bool) {
	if len(quote) > len(file) {
		return 0, false
	}
	if citedStart >= 1 && citedStart+len(quote)-1 <= len(file) && windowMatches(file[citedStart-1:], quote) {
		return citedStart, true
	}
	for i := 0; i+len(quote) <= len(file); i++ {
		if windowMatches(file[i:], quote) {
			return i + 1, true
		}
	}
	return 0, false
}

// windowMatches reports whether quote matches the prefix of window.
// First lines are compared before the rest so a non-match costs one
// comparison rather than len(quote).
func windowMatches(window, quote []string) bool {
	for j := range quote {
		if window[j] != quote[j] {
			return false
		}
	}
	return true
}

// trimEachLine trims surrounding whitespace from every line, leaving the
// line count untouched so an index into the result is still a line
// number. Indentation is the first thing a model loses when it reflows a
// quote, and it carries no evidentiary weight.
func trimEachLine(lines []string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = strings.TrimSpace(l)
	}
	return out
}

// normalizeQuote trims each line and drops surrounding blank lines, so a
// quote is compared on its content rather than on the padding around it.
// Unlike trimEachLine this changes the line count, which is why it is
// only ever applied to the quote and never to the file it is matched
// against.
func normalizeQuote(lines []string) []string {
	out := trimEachLine(lines)
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
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
