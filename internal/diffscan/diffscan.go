// Package diffscan turns a GitHub unified-diff patch fragment
// (gh.File.Patch) into per-file line coverage: which old-side and new-side
// line numbers actually appear in a hunk, and which new-side lines are pure
// additions. v1 posts review comments through the line+side API rather than
// the legacy position offset, so unlike its prototype ancestor
// (claude-code-review/src/diffmap.ts) this scanner never computes a
// hunk-relative position — only line-number coverage sets.
package diffscan

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/phatblat/agentic-review/internal/gh"
)

// Hunk is one unified-diff hunk's header coordinates.
type Hunk struct {
	OldStart int
	OldLines int
	NewStart int
	NewLines int
}

// Coverage is the per-file line coverage extracted from a unified-diff
// patch.
type Coverage struct {
	Added map[int]bool // new-side lines introduced by '+'
	Right map[int]bool // new-side lines present in any hunk ('+' and ' ')
	Left  map[int]bool // old-side lines present in any hunk ('-' and ' ')
	Hunks []Hunk
}

var hunkHeaderRE = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

// Scan parses a single file's unified-diff patch into a Coverage. Lines
// before the first hunk header are skipped; a "\ No newline at end of file"
// marker (or any other unrecognised line) advances neither side.
//
// Anchor validity (spec §6.2 "must fall within changed hunks") is
// deliberately wider than added-lines-only: context lines inside a hunk are
// valid line+side anchors too, matching what the GitHub review-comment API
// accepts. Callers check Right for ref:head anchors and Left for ref:base
// anchors.
func Scan(patch string) Coverage {
	cov := Coverage{Added: map[int]bool{}, Right: map[int]bool{}, Left: map[int]bool{}}
	if patch == "" {
		return cov
	}

	var oldLine, newLine int
	seenHunk := false

	for _, raw := range strings.Split(patch, "\n") {
		if m := hunkHeaderRE.FindStringSubmatch(raw); m != nil {
			oldLine = atoiOr(m[1], 1)
			newLine = atoiOr(m[3], 1)
			cov.Hunks = append(cov.Hunks, Hunk{
				OldStart: oldLine,
				OldLines: atoiOr(m[2], 1),
				NewStart: newLine,
				NewLines: atoiOr(m[4], 1),
			})
			seenHunk = true
			continue
		}
		if !seenHunk || raw == "" {
			continue
		}

		switch raw[0] {
		case '+':
			cov.Added[newLine] = true
			cov.Right[newLine] = true
			newLine++
		case ' ':
			cov.Right[newLine] = true
			cov.Left[oldLine] = true
			newLine++
			oldLine++
		case '-':
			cov.Left[oldLine] = true
			oldLine++
		default:
			// "\ No newline at end of file", or any other stray line:
			// counts toward nothing, advances neither side.
		}
	}
	return cov
}

func atoiOr(s string, fallback int) int {
	if s == "" {
		return fallback
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return fallback
	}
	return n
}

// ScanFiles scans every file's patch, keyed by File.Path. Files with an
// empty patch (binary, or over GitHub's per-file patch size limit) are
// omitted — their coverage is unknown, not empty.
func ScanFiles(files []gh.File) map[string]Coverage {
	out := make(map[string]Coverage, len(files))
	for _, f := range files {
		if f.Patch == "" {
			continue
		}
		out[f.Path] = Scan(f.Patch)
	}
	return out
}
