// Package render turns findings, verdicts, and run state into comment
// bodies (spec §8): the marker grammar every posted comment carries on
// its first line, and the comment bodies themselves.
package render

import (
	"fmt"
	"net/url"
	"strings"
)

// MarkerVersion is the marker grammar's own version — a separate constant
// from any payload/findings schema version (spec §8.4).
const MarkerVersion = 1

// keyOrder is the fixed, exhaustive field render order per marker kind
// (spec §8.4).
var keyOrder = map[string][]string{
	"summary": {"run", "status", "history"},
	"finding": {"fp", "run", "seq", "persona", "sev"},
	"ack":     {"run", "in-reply-to"},
}

// Marker is the parsed or to-be-rendered content of one
// `<!-- agentic-review/1 kind=... ... -->` HTML comment.
type Marker struct {
	Version int
	Kind    string
	Fields  map[string]string
}

// Render renders m as `<!-- agentic-review/1 kind={kind} {field}={value}... -->`,
// fields in kind's fixed key order (spec §8.4). A field in m.Fields but
// not in kind's fixed set is a caller bug and is silently omitted; a
// field in kind's fixed set but absent from m.Fields is also omitted
// (kind=ack's in-reply-to is the one optional field). Every value is
// url.QueryEscape'd.
func Render(m Marker) string {
	var b strings.Builder
	fmt.Fprintf(&b, "<!-- agentic-review/%d kind=%s", MarkerVersion, m.Kind)
	for _, k := range keyOrder[m.Kind] {
		if v, ok := m.Fields[k]; ok {
			fmt.Fprintf(&b, " %s=%s", k, url.QueryEscape(v))
		}
	}
	b.WriteString(" -->")
	return b.String()
}

// Parse extracts the marker from body's first line. It is strict: a
// malformed marker (wrong prefix, unparseable version, unparseable
// field) returns ok == false. Unknown field keys are preserved in
// Fields rather than rejected, since a future marker version may add
// fields this build doesn't know about.
func Parse(body string) (m Marker, ok bool) {
	line := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		line = body[:i]
	}
	line = strings.TrimSpace(line)

	const prefix, suffix = "<!-- agentic-review/", " -->"
	if !strings.HasPrefix(line, prefix) || !strings.HasSuffix(line, suffix) {
		return Marker{}, false
	}
	inner := line[len(prefix) : len(line)-len(suffix)]

	fields := strings.Fields(inner)
	if len(fields) == 0 {
		return Marker{}, false
	}
	version := 0
	if _, err := fmt.Sscanf(fields[0], "%d", &version); err != nil {
		return Marker{}, false
	}

	out := Marker{Version: version, Fields: map[string]string{}}
	for _, tok := range fields[1:] {
		k, v, found := strings.Cut(tok, "=")
		if !found {
			return Marker{}, false
		}
		decoded, err := url.QueryUnescape(v)
		if err != nil {
			return Marker{}, false
		}
		if k == "kind" {
			out.Kind = decoded
			continue
		}
		out.Fields[k] = decoded
	}
	if out.Kind == "" {
		return Marker{}, false
	}
	return out, true
}
