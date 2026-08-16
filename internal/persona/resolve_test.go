package persona

import (
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
)

func testConfig() *config.Config {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{
		"review": {Endpoint: "http://x", Model: "m", ContextWindow: 0},
		"verify": {Endpoint: "http://x", Model: "m", ContextWindow: 0},
		"triage": {Endpoint: "http://x", Model: "m", ContextWindow: 0},
	}
	return cfg
}

func builtinAndSecurity(t *testing.T) []Definition {
	t.Helper()
	defs, _, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}
	return defs
}

func TestResolveBuiltinRosterActivates(t *testing.T) {
	defs := builtinAndSecurity(t)
	reg, err := Resolve(defs, nil, testConfig())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if len(reg) != len(defs) {
		t.Fatalf("registry has %d personas, want %d", len(reg), len(defs))
	}
	if reg["logic"].Volunteer == nil {
		t.Errorf("logic.Volunteer is nil, want an always:true rule")
	}
	if reg["config-guard"].Required == nil {
		t.Errorf("config-guard.Required is nil")
	}
	if reg["verifier/duplication"].Volunteer != nil || reg["verifier/duplication"].Required != nil {
		t.Errorf("verifier/duplication should have no activation rules")
	}
}

func TestResolveRepoLocalReplacesNonImmutableBuiltin(t *testing.T) {
	defs := builtinAndSecurity(t)
	repoLocal := []Definition{{
		ID: "logic", Kind: KindAgent, Summary: "custom logic",
		Activation: Activation{Always: true},
	}}
	reg, err := Resolve(defs, repoLocal, testConfig())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if reg["logic"].Summary != "custom logic" {
		t.Errorf("logic.Summary = %q, want repo-local override to win", reg["logic"].Summary)
	}
}

func TestResolveRepoLocalCannotRedefineImmutable(t *testing.T) {
	defs := builtinAndSecurity(t)
	repoLocal := []Definition{{
		ID: "config-guard", Kind: KindDeterministic, Summary: "hijacked",
		Runtime: &Runtime{Handler: "builtin/config-guard"},
	}}
	if _, err := Resolve(defs, repoLocal, testConfig()); err == nil {
		t.Fatalf("Resolve allowed a repo-local persona to redefine an immutable builtin")
	}
}

func TestResolveRepoLocalCannotSetImmutable(t *testing.T) {
	defs := builtinAndSecurity(t)
	repoLocal := []Definition{{
		ID: "custom/thing", Kind: KindAgent, Summary: "x", Immutable: true,
		Activation: Activation{Always: true},
	}}
	if _, err := Resolve(defs, repoLocal, testConfig()); err == nil {
		t.Fatalf("Resolve allowed a repo-local persona to set immutable: true")
	}
}

func TestResolveConfigDisablesPersona(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	disabled := false
	cfg.Personas = map[string]config.PersonaOverride{"logic": {Enabled: &disabled}}
	reg, err := Resolve(defs, nil, cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if _, ok := reg["logic"]; ok {
		t.Errorf("logic is still in the registry after enabled: false")
	}
}

func TestResolveConfigCannotDisableImmutable(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	disabled := false
	cfg.Personas = map[string]config.PersonaOverride{"config-guard": {Enabled: &disabled}}
	if _, err := Resolve(defs, nil, cfg); err == nil {
		t.Fatalf("Resolve allowed disabling an immutable persona")
	}
}

func TestResolveBudgetOverrideMustLower(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	cfg.Personas = map[string]config.PersonaOverride{
		"logic": {Budget: &config.Budget{MaxTokens: 999999, MaxToolCalls: 1}},
	}
	if _, err := Resolve(defs, nil, cfg); err == nil {
		t.Fatalf("Resolve allowed raising a persona's budget")
	}
}

func TestResolveBudgetOverrideLowersSuccessfully(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	cfg.Personas = map[string]config.PersonaOverride{
		"logic": {Budget: &config.Budget{MaxTokens: 100, MaxToolCalls: 1}},
	}
	reg, err := Resolve(defs, nil, cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if reg["logic"].Budget.MaxTokens != 100 || reg["logic"].Budget.MaxToolCalls != 1 {
		t.Errorf("logic.Budget = %+v, want {100 1}", reg["logic"].Budget)
	}
}

func TestResolveHardCeilingRejectsExcessiveBudget(t *testing.T) {
	defs := []Definition{{
		ID: "huge", Kind: KindAgent, Summary: "x",
		Activation: Activation{Always: true},
		Budget:     Budget{MaxTokens: MaxTotalTokens + 1},
	}}
	if _, err := Resolve(defs, nil, testConfig()); err == nil {
		t.Fatalf("Resolve allowed a budget above the hard ceiling")
	}
}

func TestResolveOverlayRequiresOverlaysAllowed(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	// verifier/groundedness has overlays_allowed: false.
	cfg.Personas = map[string]config.PersonaOverride{"verifier/groundedness": {Overlay: "be nicer"}}
	if _, err := Resolve(defs, nil, cfg); err == nil {
		t.Fatalf("Resolve allowed an overlay on a persona with overlays_allowed: false")
	}
}

func TestResolveOverlayAppliesAndIsRetrievable(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	cfg.Personas = map[string]config.PersonaOverride{"logic": {Overlay: "Also check for X."}}
	reg, err := Resolve(defs, nil, cfg)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	builtinPrompts := builtinPromptsFor(t)
	full := reg["logic"].SystemPrompt(builtinPrompts)
	if !strings.Contains(full, "Also check for X.") || !strings.Contains(full, OverlaySeparator) {
		t.Errorf("SystemPrompt missing overlay content or separator")
	}
}

func TestResolveOverlayTooLarge(t *testing.T) {
	defs := builtinAndSecurity(t)
	cfg := testConfig()
	big := make([]byte, MaxOverlayBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	cfg.Personas = map[string]config.PersonaOverride{"logic": {Overlay: string(big)}}
	if _, err := Resolve(defs, nil, cfg); err == nil {
		t.Fatalf("Resolve allowed an overlay over MaxOverlayBytes")
	}
}

func TestResolveUnknownCapabilityErrors(t *testing.T) {
	defs := []Definition{{
		ID: "custom", Kind: KindAgent, Summary: "x",
		Activation: Activation{Always: true},
		Model:      &Model{Capability: "nonexistent"},
	}}
	if _, err := Resolve(defs, nil, testConfig()); err == nil {
		t.Fatalf("Resolve allowed a persona whose capability has no models[] binding")
	}
}

func TestResolveContextWindowTooSmall(t *testing.T) {
	defs := []Definition{{
		ID: "custom", Kind: KindAgent, Summary: "x",
		Activation: Activation{Always: true},
		Model:      &Model{Capability: "review", MinContext: "64k"},
	}}
	cfg := testConfig()
	cfg.Models["review"] = config.ModelBinding{Endpoint: "x", Model: "m", ContextWindow: 8192}
	if _, err := Resolve(defs, nil, cfg); err == nil {
		t.Fatalf("Resolve allowed min_context to exceed the configured context_window")
	}
}

func TestResolveRequiredWhenOnImmutableIsFactsOnly(t *testing.T) {
	defs := builtinAndSecurity(t)
	reg, err := Resolve(defs, nil, testConfig())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !reg["config-guard"].Required.UsesFacts {
		t.Errorf("config-guard.Required.UsesFacts = false, want true")
	}
	if reg["config-guard"].Required.UsesAssessment {
		t.Errorf("config-guard.Required.UsesAssessment = true, want false (immutable => facts-only)")
	}
}

func TestResolveVolunteerOnUsesAssessment(t *testing.T) {
	defs := builtinAndSecurity(t)
	reg, err := Resolve(defs, nil, testConfig())
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	// security's required_when references assessment.risk.
	if !reg["security"].Required.UsesAssessment {
		t.Errorf("security.Required.UsesAssessment = false, want true")
	}
}

func TestMergePromptsRepoLocalWins(t *testing.T) {
	builtin := map[string]string{"a": "builtin-a", "b": "builtin-b"}
	repoLocal := map[string]string{"a": "repo-a"}
	merged := MergePrompts(builtin, repoLocal)
	if merged["a"] != "repo-a" || merged["b"] != "builtin-b" {
		t.Errorf("merged = %+v", merged)
	}
}

func builtinPromptsFor(t *testing.T) map[string]string {
	t.Helper()
	_, prompts, err := Builtin()
	if err != nil {
		t.Fatalf("Builtin: %v", err)
	}
	return prompts
}
