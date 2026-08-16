// Package gitattributes parses the linguist-generated attribute out of a
// .gitattributes file. It is the sole authoritative signal for
// internal/classes' "generated" detector; every other input (marker
// heuristics, default globs) is a fallback for paths this package doesn't
// cover.
package gitattributes

import (
	"bufio"
	"bytes"
	"strings"

	"github.com/bmatcuk/doublestar/v4"
)

// Set holds every .gitattributes rule that sets or unsets
// linguist-generated, in file order so a later rule can override an earlier
// one for the same path — matching git's own attribute resolution, where
// the last matching pattern in the file wins.
type Set struct {
	rules []rule
}

type rule struct {
	pattern string
	value   bool
}

// Parse parses .gitattributes content, keeping only rules that mention
// linguist-generated (as a bare attribute, negated with '-', or given an
// explicit "=value"). A pattern negated with '!' explicitly unspecifies the
// attribute and is dropped rather than recorded, matching git semantics.
func Parse(data []byte) *Set {
	s := &Set{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		pattern := normalizePattern(fields[0])
		for _, attr := range fields[1:] {
			switch {
			case attr == "linguist-generated":
				s.rules = append(s.rules, rule{pattern: pattern, value: true})
			case attr == "-linguist-generated":
				s.rules = append(s.rules, rule{pattern: pattern, value: false})
			case attr == "!linguist-generated":
				// Explicitly unspecified: not recorded, so it neither sets
				// nor overrides an earlier rule for this path.
			case strings.HasPrefix(attr, "linguist-generated="):
				v := strings.TrimPrefix(attr, "linguist-generated=")
				s.rules = append(s.rules, rule{pattern: pattern, value: !strings.EqualFold(v, "false") && v != "0"})
			}
		}
	}
	return s
}

// normalizePattern mirrors gitignore/gitattributes semantics: a pattern
// with no '/' matches the basename at any depth, so it is prefixed with
// "**/" for doublestar.Match, which otherwise only matches a single path
// segment.
func normalizePattern(p string) string {
	if !strings.Contains(p, "/") {
		return "**/" + p
	}
	return p
}

// Generated reports whether path is authoritatively marked
// linguist-generated (value) by the last matching rule; ok is false when no
// rule matches path, meaning the attribute isn't specified for it.
func (s *Set) Generated(path string) (value, ok bool) {
	if s == nil {
		return false, false
	}
	for _, r := range s.rules {
		if m, _ := doublestar.Match(r.pattern, path); m {
			value, ok = r.value, true
		}
	}
	return value, ok
}
