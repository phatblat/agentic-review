// Package verify implements spec §7's verification lenses (groundedness,
// injection, duplication, materiality) and the fingerprint every finding
// is stamped with.
package verify

import (
	"crypto/sha256"
	"encoding/hex"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode"

	"github.com/phatblat/agentic-review/internal/schema"
)

// Fingerprint computes p's persona- and line-number-independent identity —
// a sha256 over the normalized anchor path, category, sorted evidence
// sources, and claim stem — deliberately excluding persona and line
// numbers so it survives rebases. Used for intra-run duplication merging
// (the duplication lens) and cross-run comment dedup (spec §6.2, §8.4).
func Fingerprint(p schema.Payload) string {
	lines := []string{
		normalizePath(p.Anchor.Path),
		p.Category,
	}
	lines = append(lines, sortedNormalizedEvidence(p.Evidence)...)
	lines = append(lines, claimStem(p.Claim))

	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// normalizePath cleans the anchor path, converts backslashes to forward
// slashes, and lowercases only the extension — paths otherwise stay
// case-sensitive.
func normalizePath(p string) string {
	cleaned := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	ext := path.Ext(cleaned)
	if ext == "" {
		return cleaned
	}
	base := strings.TrimSuffix(cleaned, ext)
	return base + strings.ToLower(ext)
}

// sortedNormalizedEvidence collapses whitespace in every evidence source
// and sorts the results lexicographically, so emission order can never
// change the hash.
func sortedNormalizedEvidence(evidence []schema.Evidence) []string {
	out := make([]string, 0, len(evidence))
	for _, e := range evidence {
		out = append(out, collapseWhitespace(e.Source))
	}
	sort.Strings(out)
	return out
}

// collapseWhitespace collapses every run of Unicode whitespace to a single
// space and trims the result.
func collapseWhitespace(s string) string {
	var b strings.Builder
	inSpace := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			if !inSpace {
				b.WriteByte(' ')
				inSpace = true
			}
			continue
		}
		inSpace = false
		b.WriteRune(r)
	}
	return strings.TrimSpace(b.String())
}

var nonStemRune = regexp.MustCompile(`[^a-z0-9 ]`)

// claimStem lowercases claim, replaces every non-[a-z0-9 ] rune with a
// space, and keeps the first 12 words.
func claimStem(claim string) string {
	replaced := nonStemRune.ReplaceAllString(strings.ToLower(claim), " ")
	fields := strings.Fields(replaced)
	if len(fields) > 12 {
		fields = fields[:12]
	}
	return strings.Join(fields, " ")
}
