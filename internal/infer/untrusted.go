package infer

import (
	"fmt"
	"strings"
)

// WrapUntrusted wraps attacker-controlled content (PR title, body, commit
// messages, diff, file contents, or a prior model turn's own findings —
// which themselves derive from untrusted PR content) in the
// <untrusted-content> tag every builtin agent/triage/verifier system
// prompt tells the model to treat as data, not instructions (spec §3.3
// decision, internal/persona/load.go's untrustedContentNotice). Any
// literal closing tag inside content is escaped so it cannot prematurely
// end the wrapper.
func WrapUntrusted(source, content string) string {
	escaped := strings.ReplaceAll(content, "</untrusted-content>", `<\/untrusted-content>`)
	return fmt.Sprintf("<untrusted-content source=%q>\n%s\n</untrusted-content>", source, escaped)
}
