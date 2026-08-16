package diffscan

import (
	"regexp"
	"strings"
)

// FileDiff is one file's patch within a multi-file unified diff, as
// produced by `git diff` or GitHub's diff format.
type FileDiff struct {
	Path  string
	Patch string
}

var diffGitHeaderRE = regexp.MustCompile(`^diff --git a/(.+) b/(.+)$`)

// SplitFiles splits a multi-file unified diff into one FileDiff per file,
// using "diff --git a/... b/..." headers as file boundaries. Used only by
// the standalone `agentic-review triage --diff` CLI path — the GitHub
// Action's live runtime always gets patches pre-split via gh.File.Patch.
// Per-file metadata lines (index/---/+++) are kept in Patch verbatim;
// Scan's own hunk-header regex already skips everything before the first
// "@@" line, so they need no separate filtering here.
func SplitFiles(diff string) []FileDiff {
	lines := strings.Split(diff, "\n")
	var files []FileDiff
	var path string
	var body []string

	flush := func() {
		if path != "" {
			files = append(files, FileDiff{Path: path, Patch: strings.Join(body, "\n")})
		}
	}

	for _, line := range lines {
		if m := diffGitHeaderRE.FindStringSubmatch(line); m != nil {
			flush()
			path = m[2]
			body = nil
			continue
		}
		if path == "" {
			continue // preamble before the first file header
		}
		body = append(body, line)
	}
	flush()
	return files
}

// CountChanges counts added and deleted content lines in patch, ignoring
// the "+++"/"---" per-file header lines that would otherwise look like
// single-line additions/deletions.
func CountChanges(patch string) (additions, deletions int) {
	for _, line := range strings.Split(patch, "\n") {
		switch {
		case strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---"):
			continue
		case strings.HasPrefix(line, "+"):
			additions++
		case strings.HasPrefix(line, "-"):
			deletions++
		}
	}
	return additions, deletions
}
