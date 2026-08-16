package manifest

import (
	"bufio"
	"bytes"
	"fmt"
	"regexp"
	"strings"
)

// goModParser hand-rolls a go.mod line parser: go.mod grammar is stable and
// small enough that a dedicated parser is not worth a dependency. Every
// require/replace line (single-line or block-form) becomes an entry keyed
// by its first token (the module path, stable identity for both
// directives); version-looking tokens on that line are extracted
// separately so a version bump can be told apart from any other edit to
// the line. Everything outside require/replace lines — the module
// directive, go/toolchain lines, blank lines, comments — must be
// byte-identical between base and head.
type goModParser struct{}

func (goModParser) Match(p string) bool { return baseName(p) == "go.mod" }

var goModVersionRE = regexp.MustCompile(`^v\d+\.\d+\.\d+(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$`)

type goModEntry struct {
	skeleton string   // the line with every version token replaced by "%v"
	versions []string // the version tokens, in line order
}

func (goModParser) VersionOnly(base, head []byte) (bool, []Change, error) {
	be, err := parseGoModEntries(base)
	if err != nil {
		return false, nil, fmt.Errorf("manifest: parse base go.mod: %w", err)
	}
	he, err := parseGoModEntries(head)
	if err != nil {
		return false, nil, fmt.Errorf("manifest: parse head go.mod: %w", err)
	}

	if stripGoModRequireReplace(base) != stripGoModRequireReplace(head) {
		return false, nil, nil
	}

	ok := true
	var changes []Change
	for _, name := range unionStringMapKeys(be, he) {
		b, bok := be[name]
		h, hok := he[name]
		if bok != hok {
			ok = false
			continue
		}
		if b.skeleton != h.skeleton || len(b.versions) != len(h.versions) {
			ok = false
			continue
		}
		for i := range b.versions {
			if b.versions[i] != h.versions[i] {
				changes = append(changes, Change{Ecosystem: "go", Name: name, From: b.versions[i], To: h.versions[i]})
			}
		}
	}
	return ok, changes, nil
}

func unionStringMapKeys(maps ...map[string]goModEntry) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	return out
}

func stripGoModComment(line string) string {
	if i := strings.Index(line, "//"); i >= 0 {
		return line[:i]
	}
	return line
}

// parseGoModEntries collects every require/replace line, single-line or
// inside a "require (" / "replace (" block.
func parseGoModEntries(data []byte) (map[string]goModEntry, error) {
	entries := map[string]goModEntry{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	inBlock := false
	for sc.Scan() {
		trimmed := strings.TrimSpace(stripGoModComment(sc.Text()))
		if trimmed == "" {
			continue
		}
		if inBlock {
			if trimmed == ")" {
				inBlock = false
				continue
			}
			addGoModLine(entries, trimmed)
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) == 0 {
			continue
		}
		if fields[0] != "require" && fields[0] != "replace" {
			continue
		}
		if len(fields) >= 2 && fields[1] == "(" {
			inBlock = true
			continue
		}
		rest := strings.TrimSpace(trimmed[len(fields[0]):])
		addGoModLine(entries, rest)
	}
	return entries, sc.Err()
}

func addGoModLine(entries map[string]goModEntry, line string) {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return
	}
	key := fields[0]
	var versions []string
	skeleton := make([]string, len(fields))
	for i, f := range fields {
		if goModVersionRE.MatchString(f) {
			versions = append(versions, f)
			skeleton[i] = "%v"
		} else {
			skeleton[i] = f
		}
	}
	entries[key] = goModEntry{skeleton: strings.Join(skeleton, " "), versions: versions}
}

// stripGoModRequireReplace returns data with every require/replace
// single-line or block removed, for a byte-equality check that everything
// else in the file is unchanged.
func stripGoModRequireReplace(data []byte) string {
	var kept []string
	sc := bufio.NewScanner(bytes.NewReader(data))
	inBlock := false
	for sc.Scan() {
		raw := sc.Text()
		trimmed := strings.TrimSpace(stripGoModComment(raw))
		if inBlock {
			if trimmed == ")" {
				inBlock = false
			}
			continue
		}
		fields := strings.Fields(trimmed)
		if len(fields) >= 1 && (fields[0] == "require" || fields[0] == "replace") {
			if len(fields) >= 2 && fields[1] == "(" {
				inBlock = true
			}
			continue
		}
		kept = append(kept, raw)
	}
	return strings.Join(kept, "\n")
}
