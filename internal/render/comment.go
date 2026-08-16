package render

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/phatblat/agentic-review/internal/schema"
)

// severityEmoji is spec §6.2's severity table.
var severityEmoji = map[string]string{
	"blocker": "⛔️",
	"error":   "🚨",
	"warning": "⚠️",
	"nit":     "🧼",
}

// severityOrder is the fixed display order for the counts line and the
// per-severity cap footer.
var severityOrder = []string{"blocker", "error", "warning", "nit"}

// withheldNotice replaces title and claim for an injection-dropped finding
// (spec §8.3): its content is never re-rendered anywhere, including the
// filtered-findings detail row.
const withheldNotice = "*(content withheld)*"

// FindingComment renders one line/file review comment body (spec §8.2):
//
//	<marker>
//	{emoji} **{title}** · `{category}` · {persona}
//
//	{claim}
//
//	{suggestion}
//
// runID identifies this comment's originating run for the marker; seq is
// always "1" in v1 (no persona ever emits the same finding twice within
// one run — duplication has already merged those before render sees
// them). suggestedFixLang names the anchor file's language for the
// fork-PR plain fence (e.g. "go"); "" renders a bare fence.
func FindingComment(f schema.Finding, runID int64, isFork bool, suggestedFixLang string) string {
	marker := Render(Marker{Kind: "finding", Fields: map[string]string{
		"fp":      f.Envelope.Fingerprint,
		"run":     strconv.FormatInt(runID, 10),
		"seq":     "1",
		"persona": f.Envelope.Persona,
		"sev":     f.Payload.Severity,
	}})

	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("\n")
	fmt.Fprintf(&b, "%s **%s** · `%s` · %s\n\n", severityEmoji[f.Payload.Severity], f.Payload.Title, f.Payload.Category, f.Envelope.Persona)
	b.WriteString(f.Payload.Claim)

	if suggestion := renderSuggestion(f, isFork, suggestedFixLang); suggestion != "" {
		b.WriteString("\n\n")
		b.WriteString(suggestion)
	}
	return b.String()
}

// renderSuggestion renders f.Payload.SuggestedFix as a committable
// ```suggestion``` fence on an internal PR, or a plain fence titled
// "Suggested change (not applyable on fork PRs)" on a fork — "applyable"
// is never a valid GitHub suggestion outside the PR's own repository, so
// forks never get one (spec item 38, unconditional per M7 item 34's
// mechanical validation).
func renderSuggestion(f schema.Finding, isFork bool, lang string) string {
	sf := f.Payload.SuggestedFix
	if sf == nil {
		return ""
	}
	if isFork {
		return fmt.Sprintf("Suggested change (not applyable on fork PRs):\n```%s\n%s\n```", lang, sf.Replacement)
	}
	return fmt.Sprintf("```suggestion\n%s\n```", sf.Replacement)
}

// FilteredEntry is one row in the summary's "Filtered findings" details
// block: a finding the duplication, injection, groundedness, or
// materiality lens dropped, downgraded, or merged away. Cap-suppressed
// findings are not listed here — only counted in the footer (spec item
// 38 names a details row only for "dropped/downgraded" findings).
type FilteredEntry struct {
	Severity string
	Persona  string
	Title    string // withheldNotice rendered in place of this when Injected is true
	Lens     string
	Reason   string
	Injected bool
}

// HistoryEntry is one prior run recorded in the summary marker's history
// field (spec item 38).
type HistoryEntry struct {
	Run     int64    `json:"run"`
	URL     string   `json:"url"`
	Trigger string   `json:"trigger"`
	Team    []string `json:"team"`
	Tokens  int      `json:"tokens"`
	MS      int64    `json:"ms"`
}

// maxHistoryEntries caps the summary marker's history array so the
// rendered marker cannot push the comment body past GitHub's 65,536
// character limit (spec item 38, decision).
const maxHistoryEntries = 10

// EncodeHistory renders entries (newest first; the caller has already
// prepended this run's own entry) as the summary marker's history field
// value: url.QueryEscape of the compact JSON array, capped to the most
// recent maxHistoryEntries.
func EncodeHistory(entries []HistoryEntry) (string, error) {
	if len(entries) > maxHistoryEntries {
		entries = entries[:maxHistoryEntries]
	}
	data, err := json.Marshal(entries)
	if err != nil {
		return "", fmt.Errorf("render: encode history: %w", err)
	}
	return string(data), nil
}

// SummaryInput bundles every already-decided piece of state the summary
// comment needs. internal/post partitions findings (individually-posted
// vs. not-on-changed-lines vs. filtered) and applies per-severity caps
// before calling Summary; render itself makes no such decisions.
type SummaryInput struct {
	RunID   int64
	RunURL  string
	Trigger string

	// SkipReason, when non-empty, renders the tier-0 skip variant and
	// every other field below is ignored.
	SkipReason string

	// ErrorStage, when non-empty, renders the error variant: the failed
	// stage name, whether the fallback roster ran, and a run-log link.
	ErrorStage         string
	FallbackRosterUsed bool

	// Counts is every accepted finding's severity tally (both
	// individually-posted and not-on-changed-lines), keyed by severity.
	Counts map[string]int
	// NotOnChangedLines are pr-anchored findings and file-anchored
	// findings whose path is absent from the diff — never posted as
	// their own comment, so their full body renders here instead.
	NotOnChangedLines []schema.Finding
	IsFork            bool
	// SuggestedFixLangs maps an anchor path to its language, used when
	// rendering a NotOnChangedLines finding's suggested fix.
	SuggestedFixLangs map[string]string

	Filtered        []FilteredEntry
	SuppressedByCap int
	Team            []TeamMember
	TotalTokens     int
	Duration        time.Duration
	History         []HistoryEntry
}

// TeamMember is one roster member credited in the summary footer's "👥"
// line.
type TeamMember struct {
	ID            string
	ResolvedModel string // "" for a deterministic persona
}

// Summary renders the single upserted summary comment (spec §8.2, §16).
func Summary(in SummaryInput) (string, error) {
	history, err := EncodeHistory(in.History)
	if err != nil {
		return "", err
	}
	status := summaryStatus(in)
	marker := Render(Marker{Kind: "summary", Fields: map[string]string{
		"run": strconv.FormatInt(in.RunID, 10), "status": status, "history": history,
	}})

	var b strings.Builder
	b.WriteString(marker)
	b.WriteString("\n## agentic-review\n\n")

	switch {
	case in.SkipReason != "":
		fmt.Fprintf(&b, "✅ skipped agentic review: %s\n\n---\n🔢 0 tokens\n", in.SkipReason)
		return b.String(), nil
	case in.ErrorStage != "":
		fmt.Fprintf(&b, "❌ %s failed", in.ErrorStage)
		if in.FallbackRosterUsed {
			b.WriteString(" (fallback roster ran)")
		}
		b.WriteString("\n\n---\n")
		writeFooter(&b, in)
		if in.RunURL != "" {
			fmt.Fprintf(&b, "\n📋 [run log](%s)", in.RunURL)
		}
		b.WriteString("\n")
		return b.String(), nil
	case hasNothingToShow(in):
		b.WriteString("✅ No findings\n\n---\n")
		writeFooter(&b, in)
		b.WriteString("\n")
		return b.String(), nil
	}

	writeCountsLine(&b, in.Counts)

	if len(in.NotOnChangedLines) > 0 {
		b.WriteString("\n### Findings not on changed lines\n\n")
		for _, f := range in.NotOnChangedLines {
			lang := in.SuggestedFixLangs[f.Payload.Anchor.Path]
			writeInlineFinding(&b, f, in.IsFork, lang)
			b.WriteString("\n")
		}
	}

	if len(in.Filtered) > 0 {
		fmt.Fprintf(&b, "\n<details><summary>Filtered findings (%d)</summary>\n\n", len(in.Filtered))
		for _, e := range in.Filtered {
			title := e.Title
			if e.Injected {
				title = withheldNotice
			}
			fmt.Fprintf(&b, "- %s %s — %s: %s: %s\n", severityEmoji[e.Severity], e.Persona, title, e.Lens, e.Reason)
		}
		b.WriteString("</details>\n")
	}

	b.WriteString("\n---\n")
	writeFooter(&b, in)
	if in.SuppressedByCap > 0 {
		fmt.Fprintf(&b, "\n%d findings suppressed by cap", in.SuppressedByCap)
	}
	b.WriteString("\n")
	return b.String(), nil
}

func summaryStatus(in SummaryInput) string {
	switch {
	case in.SkipReason != "":
		return "skipped"
	case in.ErrorStage != "":
		return "error"
	case hasNothingToShow(in):
		return "clean"
	default:
		return "findings"
	}
}

func hasNothingToShow(in SummaryInput) bool {
	return totalCount(in.Counts) == 0 && len(in.NotOnChangedLines) == 0 && len(in.Filtered) == 0 && in.SuppressedByCap == 0
}

func totalCount(counts map[string]int) int {
	n := 0
	for _, c := range counts {
		n += c
	}
	return n
}

func writeCountsLine(b *strings.Builder, counts map[string]int) {
	parts := make([]string, 0, len(severityOrder))
	for _, sev := range severityOrder {
		parts = append(parts, fmt.Sprintf("%s %d", severityEmoji[sev], counts[sev]))
	}
	b.WriteString(strings.Join(parts, " · "))
	b.WriteString("\n")
}

// writeInlineFinding renders one not-on-changed-lines finding's full body
// (path:start-end, an evidence fence, the claim) for the summary section.
func writeInlineFinding(b *strings.Builder, f schema.Finding, isFork bool, lang string) {
	fmt.Fprintf(b, "%s **%s** · `%s` · %s\n", severityEmoji[f.Payload.Severity], f.Payload.Title, f.Payload.Category, f.Envelope.Persona)
	if f.Payload.Anchor.Path != "" {
		if f.Payload.Anchor.StartLine > 0 {
			fmt.Fprintf(b, "`%s:%d-%d`\n", f.Payload.Anchor.Path, f.Payload.Anchor.StartLine, f.Payload.Anchor.EndLine)
		} else {
			fmt.Fprintf(b, "`%s`\n", f.Payload.Anchor.Path)
		}
	}
	for _, e := range f.Payload.Evidence {
		fmt.Fprintf(b, "```%s\n%s\n```\n", lang, e.Source)
	}
	b.WriteString(f.Payload.Claim)
	b.WriteString("\n")
	if suggestion := renderSuggestion(f, isFork, lang); suggestion != "" {
		b.WriteString("\n")
		b.WriteString(suggestion)
		b.WriteString("\n")
	}
}

func writeFooter(b *strings.Builder, in SummaryInput) {
	if len(in.Team) > 0 {
		parts := make([]string, 0, len(in.Team))
		for _, m := range in.Team {
			if m.ResolvedModel == "" {
				parts = append(parts, fmt.Sprintf("%s (deterministic)", m.ID))
			} else {
				parts = append(parts, fmt.Sprintf("%s (%s)", m.ID, m.ResolvedModel))
			}
		}
		fmt.Fprintf(b, "👥 %s\n", strings.Join(parts, " · "))
	}
	fmt.Fprintf(b, "🔢 %s tokens · ⏱ %s", formatTokens(in.TotalTokens), formatDuration(in.Duration))
}

// formatTokens renders n with thousands separators (e.g. "41,203").
func formatTokens(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

// formatDuration renders d as "{m}m {s}s".
func formatDuration(d time.Duration) string {
	total := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%dm %ds", total/60, total%60)
}
