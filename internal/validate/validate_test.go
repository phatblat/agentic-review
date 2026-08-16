package validate

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/persona"
)

func resolvedPersona(id string, kind persona.Kind, groups ...activation.Trigger) *persona.ResolvedPersona {
	return &persona.ResolvedPersona{Definition: persona.Definition{
		ID: id, Kind: kind, Activation: persona.Activation{VolunteerOn: groups},
	}}
}

func TestAllChecksEveryVolunteerOnGroupNotJustFirst(t *testing.T) {
	reg := persona.Registry{
		"security": resolvedPersona("security", persona.KindAgent,
			activation.Trigger{Paths: []string{"**/auth/**"}},
			activation.Trigger{Domains: []string{"secrets"}},
		),
	}
	checks, err := All(reg, config.Defaults())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	var slots []string
	for _, c := range checks {
		slots = append(slots, c.Slot)
	}
	want := []string{"security.volunteer_on[0]", "security.volunteer_on[1]"}
	if len(slots) != len(want) {
		t.Fatalf("slots = %v, want %v", slots, want)
	}
	for i, s := range want {
		if slots[i] != s {
			t.Errorf("slots[%d] = %q, want %q", i, slots[i], s)
		}
	}
	for _, c := range checks {
		if c.Err != nil {
			t.Errorf("check %s failed unexpectedly: %v", c.Slot, c.Err)
		}
	}
}

func TestAllSkipsNonTeamKindPersonas(t *testing.T) {
	reg := persona.Registry{
		"verifier/groundedness": resolvedPersona("verifier/groundedness", persona.KindVerifier,
			activation.Trigger{Paths: []string{"**"}},
		),
		"triage": resolvedPersona("triage", persona.KindTriage),
	}
	checks, err := All(reg, config.Defaults())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(checks) != 0 {
		t.Errorf("checks = %+v, want none: verifier/triage kinds never volunteer for the team", checks)
	}
}

func TestAllCatchesMalformedVolunteerOnExpr(t *testing.T) {
	reg := persona.Registry{
		"broken": resolvedPersona("broken", persona.KindAgent,
			activation.Trigger{Expr: "this is not ) valid cel("},
		),
	}
	checks, err := All(reg, config.Defaults())
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(checks) != 1 || checks[0].Err == nil {
		t.Fatalf("checks = %+v, want the malformed expression to fail", checks)
	}
}

func TestAllChecksEveryEscalationRule(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Escalation = []config.Escalation{
		{When: "facts.diff.additions > 500", Add: []string{"architecture"}},
		{When: "assessment.risk >= RISK_HIGH", Add: []string{"security"}},
	}
	checks, err := All(persona.Registry{}, cfg)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %+v, want both escalation rules checked", checks)
	}
	for _, c := range checks {
		if c.Err != nil {
			t.Errorf("escalation check %s failed: %v", c.Slot, c.Err)
		}
	}
}

func TestAllCatchesSkipWhenReferencingAssessment(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{"assessment.risk >= RISK_HIGH"} // skip_when is facts-only
	checks, err := All(persona.Registry{}, cfg)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(checks) != 1 || checks[0].Err == nil {
		t.Fatalf("checks = %+v, want a context-class lint error (skip_when is facts-only)", checks)
	}
}

func TestAllRunsExhaustivelyPastAFirstError(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{
		"assessment.risk >= RISK_HIGH", // invalid: facts-only slot referencing assessment
		"facts.diff.additions > 500",   // valid
	}
	checks, err := All(persona.Registry{}, cfg)
	if err != nil {
		t.Fatalf("All: %v", err)
	}
	if len(checks) != 2 {
		t.Fatalf("checks = %+v, want both skip_when entries checked despite the first failing", checks)
	}
	if checks[0].Err == nil {
		t.Errorf("checks[0] = %+v, want an error", checks[0])
	}
	if checks[1].Err != nil {
		t.Errorf("checks[1] = %+v, want it still checked and passing", checks[1])
	}
}
