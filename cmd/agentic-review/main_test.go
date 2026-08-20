package main

import "testing"

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
