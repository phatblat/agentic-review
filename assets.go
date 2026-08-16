// Package agenticreview holds compile-time embedded assets that must live
// at the repository root per docs/spec.md's layout — one directory above
// every package that consumes them. Go's //go:embed can only reach its own
// package directory or subdirectories, never upward, so this thin root
// package exists solely to bridge that gap; it has no other logic.
package agenticreview

import "embed"

// BuiltinPersonasFS embeds the builtin personas/ and prompts/ trees,
// consumed by internal/persona.
//
//go:embed personas prompts
var BuiltinPersonasFS embed.FS
