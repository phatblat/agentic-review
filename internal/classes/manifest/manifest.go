// Package manifest performs per-ecosystem structural manifest diffing:
// whether a change to a dependency manifest (Cargo.toml, package.json,
// deno.json(c), go.mod) is confined to dependency version specs or the
// package's own version. Text diffs are never trusted — every parser works
// on the decoded document structure.
package manifest

import "path"

// Change is one dependency version change observed by VersionOnly.
type Change struct {
	Ecosystem, Name, From, To string
}

// Parser is one ecosystem's manifest structural differ.
type Parser interface {
	// Match reports whether path is this ecosystem's manifest file.
	Match(path string) bool
	// VersionOnly reports whether base→head changes only dependency version
	// specs or the package's own version, and returns the dependency
	// changes it observed. Any parse error, unknown key added/removed, or
	// non-version value change means ok=false.
	VersionOnly(base, head []byte) (ok bool, changes []Change, err error)
}

var parsers = []Parser{cargoParser{}, npmParser{}, denoParser{}, goModParser{}}

// For returns the first parser whose Match matches p, or nil if none does.
func For(p string) Parser {
	for _, parser := range parsers {
		if parser.Match(p) {
			return parser
		}
	}
	return nil
}

func baseName(p string) string { return path.Base(p) }
