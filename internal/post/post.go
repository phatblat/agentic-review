// Package post turns a run's final findings into GitHub API calls (spec
// §8.1, §8.2, §8.4): the line/file comment rendering ladder, cross-run
// dedup, per-severity caps, and the upserted summary comment. It owns
// every finding-placement decision; internal/render only formats text
// given post's already-decided inputs.
package post

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/render"
	"github.com/phatblat/agentic-review/internal/schema"
)

// Input bundles everything Post needs to place every accepted finding and
// upsert the summary comment.
type Input struct {
	Port    gh.Port
	Repo    gh.Repo
	Number  int
	HeadSHA string
	IsFork  bool

	RunID   int64
	RunURL  string
	Trigger string

	// Findings is every finding from findings.raw.json — post filters by
	// envelope.verification.disposition itself, so callers pass the
	// unfiltered set.
	Findings []schema.Finding
	// DiffPaths is the set of paths in this run's diff — a file-anchored
	// finding whose path is absent renders in the summary instead of an
	// individual comment (spec item 39).
	DiffPaths map[string]bool
	// Caps is cfg.Review.Caps: a per-severity ceiling on individually
	// posted comments; "blocker" is never consulted (uncapped).
	Caps map[string]int

	Team        []render.TeamMember
	TotalTokens int
	Duration    time.Duration
	History     []render.HistoryEntry

	// SkipReason, ErrorStage, and FallbackRosterUsed select the summary
	// variant exactly as render.SummaryInput does.
	SkipReason         string
	ErrorStage         string
	FallbackRosterUsed bool
}

// Post lists existing review comments for dedup, places every accepted
// finding via the rendering ladder, and upserts the summary comment.
func Post(ctx context.Context, in Input) error {
	existing, err := in.Port.ListReviewComments(ctx, in.Repo, in.Number)
	if err != nil {
		return fmt.Errorf("post: list review comments: %w", err)
	}
	postedFPs := postedFingerprints(existing)

	var lineFindings, fileFindings, notOnChangedLines []schema.Finding
	var filtered []render.FilteredEntry

	for _, f := range in.Findings {
		switch f.Envelope.Verification.Disposition {
		case schema.DispositionDropped, schema.DispositionDowngraded, schema.DispositionMerged:
			filtered = append(filtered, filteredEntry(f))
			continue
		case schema.DispositionAccepted:
			// falls through to placement below
		default:
			continue // unknown disposition; never posted
		}

		switch f.Payload.Anchor.Kind {
		case schema.AnchorLine:
			lineFindings = append(lineFindings, f)
		case schema.AnchorFile:
			if in.DiffPaths[f.Payload.Anchor.Path] {
				fileFindings = append(fileFindings, f)
			} else {
				notOnChangedLines = append(notOnChangedLines, f)
			}
		default: // AnchorPR
			notOnChangedLines = append(notOnChangedLines, f)
		}
	}

	lineFindings, capSuppressed1 := dedupAndCap(lineFindings, postedFPs, in.Caps)
	fileFindings, capSuppressed2 := dedupAndCap(fileFindings, postedFPs, in.Caps)

	if len(lineFindings) > 0 {
		if err := postLineFindings(ctx, in, lineFindings); err != nil {
			return err
		}
	}
	for _, f := range fileFindings {
		body := render.FindingComment(f, in.RunID, in.IsFork, facts.LanguageOf(f.Payload.Anchor.Path))
		if err := in.Port.CreateFileComment(ctx, in.Repo, in.Number, in.HeadSHA, f.Payload.Anchor.Path, body); err != nil {
			return fmt.Errorf("post: create file comment %s: %w", f.Payload.Anchor.Path, err)
		}
	}

	counts := SeverityCounts(in.Findings)
	langs := make(map[string]string, len(notOnChangedLines))
	for _, f := range notOnChangedLines {
		langs[f.Payload.Anchor.Path] = facts.LanguageOf(f.Payload.Anchor.Path)
	}

	summary, err := render.Summary(render.SummaryInput{
		RunID: in.RunID, RunURL: in.RunURL, Trigger: in.Trigger,
		SkipReason: in.SkipReason, ErrorStage: in.ErrorStage, FallbackRosterUsed: in.FallbackRosterUsed,
		Counts: counts, NotOnChangedLines: notOnChangedLines, IsFork: in.IsFork, SuggestedFixLangs: langs,
		Filtered: filtered, SuppressedByCap: capSuppressed1 + capSuppressed2,
		Team: in.Team, TotalTokens: in.TotalTokens, Duration: in.Duration, History: in.History,
	})
	if err != nil {
		return fmt.Errorf("post: render summary: %w", err)
	}
	return upsertSummary(ctx, in, summary)
}

// postedFingerprints parses every kind=finding marker among existing
// review comments into a fingerprint set for dedup.
func postedFingerprints(existing []gh.Comment) map[string]bool {
	out := map[string]bool{}
	for _, c := range existing {
		m, ok := render.Parse(c.Body)
		if !ok || m.Kind != "finding" {
			continue
		}
		if fp := m.Fields["fp"]; fp != "" {
			out[fp] = true
		}
	}
	return out
}

// dedupAndCap drops findings whose fingerprint was already posted in a
// prior run, then applies cfg.Review.Caps per severity (blocker
// unconstrained), taking findings in envelope.id order (already the
// team's emission order) and suppressing the remainder. It returns the
// postable findings and how many were suppressed by a cap (dedup drops
// are not counted — they were never new work, not a suppression).
func dedupAndCap(findings []schema.Finding, postedFPs map[string]bool, caps map[string]int) ([]schema.Finding, int) {
	var deduped []schema.Finding
	for _, f := range findings {
		if postedFPs[f.Envelope.Fingerprint] {
			continue
		}
		deduped = append(deduped, f)
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].Envelope.ID < deduped[j].Envelope.ID })

	bySeverity := map[string][]schema.Finding{}
	for _, f := range deduped {
		bySeverity[f.Payload.Severity] = append(bySeverity[f.Payload.Severity], f)
	}

	var out []schema.Finding
	suppressed := 0
	for _, sev := range schema.Severities {
		bucket := bySeverity[sev]
		if sev == "blocker" {
			out = append(out, bucket...)
			continue
		}
		limit := caps[sev]
		if limit <= 0 || len(bucket) <= limit {
			out = append(out, bucket...)
			continue
		}
		out = append(out, bucket[:limit]...)
		suppressed += len(bucket) - limit
	}
	return out, suppressed
}

// postLineFindings batches every line-anchored finding into one
// CreateReview call (Event "COMMENT", empty review body) so reviewers get
// a single notification rather than one per finding (spec item 39).
func postLineFindings(ctx context.Context, in Input, findings []schema.Finding) error {
	comments := make([]gh.ReviewComment, 0, len(findings))
	for _, f := range findings {
		a := f.Payload.Anchor
		side := "RIGHT"
		if a.Ref == schema.RefBase {
			side = "LEFT"
		}
		rc := gh.ReviewComment{
			Path: a.Path, Line: a.EndLine, Side: side,
			Body: render.FindingComment(f, in.RunID, in.IsFork, facts.LanguageOf(a.Path)),
		}
		if a.StartLine != a.EndLine {
			rc.StartLine = a.StartLine
		}
		comments = append(comments, rc)
	}
	if err := in.Port.CreateReview(ctx, in.Repo, in.Number, in.HeadSHA, "", comments); err != nil {
		return fmt.Errorf("post: create review: %w", err)
	}
	return nil
}

// upsertSummary edits the first existing kind=summary issue comment, or
// creates one if none exists (spec item 39, §16).
func upsertSummary(ctx context.Context, in Input, body string) error {
	existing, err := in.Port.ListIssueComments(ctx, in.Repo, in.Number)
	if err != nil {
		return fmt.Errorf("post: list issue comments: %w", err)
	}
	for _, c := range existing {
		m, ok := render.Parse(c.Body)
		if ok && m.Kind == "summary" {
			if err := in.Port.EditIssueComment(ctx, in.Repo, c.ID, body); err != nil {
				return fmt.Errorf("post: edit summary comment: %w", err)
			}
			return nil
		}
	}
	if _, err := in.Port.CreateIssueComment(ctx, in.Repo, in.Number, body); err != nil {
		return fmt.Errorf("post: create summary comment: %w", err)
	}
	return nil
}

// SeverityCounts tallies accepted findings by severity (blocker, error,
// warning, nit), zero-filled for severities with no accepted findings.
// Exported for reuse by internal/runner.Review when computing the
// action.yml output values (spec item 46).
func SeverityCounts(findings []schema.Finding) map[string]int {
	counts := make(map[string]int, len(schema.Severities))
	for _, sev := range schema.Severities {
		counts[sev] = 0
	}
	for _, f := range findings {
		if f.Envelope.Verification.Disposition == schema.DispositionAccepted {
			counts[f.Payload.Severity]++
		}
	}
	return counts
}

func filteredEntry(f schema.Finding) render.FilteredEntry {
	lens, reason := "", ""
	if n := len(f.Envelope.Verification.Verdicts); n > 0 {
		last := f.Envelope.Verification.Verdicts[n-1]
		lens, reason = last.Lens, last.Reason
	}
	injected := f.Envelope.Verification.Disposition == schema.DispositionDropped && lens == "injection"
	entry := render.FilteredEntry{
		Severity: f.Payload.Severity, Persona: f.Envelope.Persona, Title: f.Payload.Title,
		Lens: lens, Reason: reason, Injected: injected,
	}
	if injected {
		logx.Debug("post: %s: withholding injection-dropped content from the summary", f.Envelope.ID)
	}
	return entry
}
