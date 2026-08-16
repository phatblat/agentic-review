package persona

import agenticreview "github.com/phatblat/agentic-review"

// Builtin returns the builtin persona definitions and their raw system
// prompt text — the exact spec §14 roster (triage, logic, security,
// fork-guard, config-guard, dep-risk,
// verifier/{groundedness,materiality,duplication,injection}); config-guard
// and verifier/injection are immutable — loaded from the repo-root
// personas/ and prompts/ trees embedded in agenticreview.BuiltinPersonasFS.
func Builtin() ([]Definition, map[string]string, error) {
	return LoadDir(agenticreview.BuiltinPersonasFS, "personas")
}
