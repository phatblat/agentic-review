package persona

import (
	"sort"
	"strings"
	"testing"
)

func TestBuiltinRoster(t *testing.T) {
	defs, prompts, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}

	ids := make([]string, len(defs))
	byID := make(map[string]Definition, len(defs))
	for i, d := range defs {
		ids[i] = d.ID
		byID[d.ID] = d
	}
	sort.Strings(ids)

	want := []string{
		"config-guard", "dep-risk", "fork-guard", "logic", "security", "triage",
		"verifier/duplication", "verifier/groundedness", "verifier/injection", "verifier/materiality",
	}
	if len(ids) != len(want) {
		t.Fatalf("loaded %d personas %v, want %d: %v", len(ids), ids, len(want), want)
	}
	for i, id := range want {
		if ids[i] != id {
			t.Errorf("ids[%d] = %q, want %q (full list: %v)", i, ids[i], id, ids)
		}
	}

	if !byID["config-guard"].Immutable {
		t.Errorf("config-guard.Immutable = false, want true")
	}
	if !byID["verifier/injection"].Immutable {
		t.Errorf("verifier/injection.Immutable = false, want true")
	}
	for _, id := range []string{"logic", "security", "fork-guard", "dep-risk", "triage", "verifier/groundedness", "verifier/materiality", "verifier/duplication"} {
		if byID[id].Immutable {
			t.Errorf("%s.Immutable = true, want false", id)
		}
	}

	if byID["triage"].Kind != KindTriage {
		t.Errorf("triage.Kind = %s, want triage", byID["triage"].Kind)
	}
	if byID["dep-risk"].Kind != KindDeterministic || byID["config-guard"].Kind != KindDeterministic {
		t.Errorf("dep-risk/config-guard kind = %s/%s, want deterministic",
			byID["dep-risk"].Kind, byID["config-guard"].Kind)
	}
	for _, id := range []string{"verifier/groundedness", "verifier/materiality", "verifier/duplication", "verifier/injection"} {
		if byID[id].Kind != KindVerifier {
			t.Errorf("%s.Kind = %s, want verifier", id, byID[id].Kind)
		}
	}
	for _, id := range []string{"logic", "security", "fork-guard"} {
		if byID[id].Kind != KindAgent {
			t.Errorf("%s.Kind = %s, want agent", id, byID[id].Kind)
		}
	}

	// Every persona with a prompt.system field must have loaded prompt text
	// carrying the untrusted-content notice; dep-risk and
	// verifier/duplication call no model and must have neither.
	wantPrompt := map[string]bool{
		"triage": true, "logic": true, "security": true, "fork-guard": true,
		"config-guard": true, "verifier/groundedness": true, "verifier/materiality": true,
		"verifier/injection": true, "dep-risk": false, "verifier/duplication": false,
	}
	for id, want := range wantPrompt {
		text, ok := prompts[id]
		if ok != want {
			t.Errorf("prompts[%q] present = %v, want %v", id, ok, want)
			continue
		}
		if want && !strings.Contains(text, "untrusted-content") {
			t.Errorf("prompts[%q] missing the untrusted-content notice", id)
		}
	}
}
