package main

import (
	"os"
	"regexp"
	"testing"
)

func TestInferenceHeaders(t *testing.T) {
	t.Run("GITHUB_RUN_ID set", func(t *testing.T) {
		t.Setenv("GITHUB_RUN_ID", "42")
		h := inferenceHeaders()
		if got := h.Get("x-session-id"); got != "42" {
			t.Errorf("x-session-id = %q, want \"42\"", got)
		}
	})

	t.Run("GITHUB_RUN_ID empty", func(t *testing.T) {
		t.Setenv("GITHUB_RUN_ID", "")
		h := inferenceHeaders()
		if got := h.Get("x-session-id"); got != "" {
			t.Errorf("x-session-id = %q, want no header for an empty GITHUB_RUN_ID", got)
		}
	})
}

// TestVersionMatchesShim fails when main.go's version literal and the
// shim's EXPECTED_VERSION drift apart: the shim trusts only a binary whose
// `version` output equals EXPECTED_VERSION, and dogfood.yml and the
// docs/setup.md consumer workflow rely on a plain `go build` passing that
// check. `just version X.Y.Z` rewrites both.
func TestVersionMatchesShim(t *testing.T) {
	src, err := os.ReadFile("../../shim/index.mjs")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^export const EXPECTED_VERSION = "([^"]*)";$`).FindSubmatch(src)
	if m == nil {
		t.Fatal(`shim/index.mjs has no line matching export const EXPECTED_VERSION = "...";`)
	}
	if want := string(m[1]); version != want {
		t.Errorf("main.version = %q, shim EXPECTED_VERSION = %q; run `just version %s`", version, want, want)
	}
}
