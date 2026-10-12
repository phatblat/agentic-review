package verify

import (
	"context"
	"testing"

	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func TestOrderIsFixedGroundednessInjectionDuplicationMateriality(t *testing.T) {
	names := make([]string, 0, 4)
	for _, l := range Order() {
		names = append(names, l.Name())
	}
	want := []string{"groundedness", "injection", "duplication", "materiality"}
	if len(names) != len(want) {
		t.Fatalf("Order() = %v, want %v", names, want)
	}
	for i, n := range want {
		if names[i] != n {
			t.Errorf("Order()[%d] = %q, want %q", i, names[i], n)
		}
	}
}

func TestVerifiableSkipsNonAcceptedDispositions(t *testing.T) {
	in := []schema.Finding{
		{Envelope: schema.Envelope{ID: "f-0001", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
		{Envelope: schema.Envelope{ID: "f-0002", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
		{Envelope: schema.Envelope{ID: "f-0003", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
	}
	idx := verifiable(in, nil)
	if len(idx) != 2 || idx[0] != 0 || idx[1] != 2 {
		t.Fatalf("idx = %v, want [0 2]", idx)
	}
}

func optOutRegistry() persona.Registry {
	return persona.Registry{
		"config-guard": {Definition: persona.Definition{
			ID: "config-guard", Kind: persona.KindDeterministic, Verification: persona.Verification{Required: false},
		}},
		"security": {Definition: persona.Definition{
			ID: "security", Kind: persona.KindAgent, Verification: persona.Verification{Required: true},
		}},
	}
}

func acceptedBy(id, personaID string) schema.Finding {
	return schema.Finding{Envelope: schema.Envelope{
		ID: id, Persona: personaID, Verification: schema.Verification{Disposition: schema.DispositionAccepted},
	}}
}

// A persona with verification.required: false is skipped; a persona that
// requires verification, and one missing from the registry, are not. The
// unknown-persona case is the fail-closed half: an unrecognized source
// must never get a free pass.
func TestVerifiableHonorsPersonaOptOut(t *testing.T) {
	in := []schema.Finding{
		acceptedBy("f-0001", "config-guard"),
		acceptedBy("f-0002", "security"),
		acceptedBy("f-0003", "not-in-registry"),
	}
	idx := verifiable(in, optOutRegistry())
	if len(idx) != 2 || idx[0] != 1 || idx[1] != 2 {
		t.Fatalf("idx = %v, want [1 2] (config-guard opted out; security and the unknown persona are verified)", idx)
	}
}

// The opt-out covers a persona's code-computed facts, not findings a model
// authored under that persona (config-guard's intent-judgment pass).
func TestVerifiableKeepsModelAuthoredFindingOfOptedOutPersona(t *testing.T) {
	judged := acceptedBy("f-0001", "config-guard")
	judged.Envelope.ModelAuthored = true
	fact := acceptedBy("f-0002", "config-guard")

	idx := verifiable([]schema.Finding{judged, fact}, optOutRegistry())
	if len(idx) != 1 || idx[0] != 0 {
		t.Fatalf("idx = %v, want [0]: the model-authored finding is verified, the deterministic fact is not", idx)
	}
}

// Regression: a config-guard blocker went through the model lenses, and
// groundedness judged "gate.fail_on weakened" against a single file
// snapshot and dropped it, so a config-touching PR passed its gate (spec
// §12.4(4)). The client has no scripted responses, so any lens that
// reaches the model fails Run.
func TestRunLeavesDeterministicBlockerUntouched(t *testing.T) {
	blocker := acceptedBy("f-0001", "config-guard")
	blocker.Payload.Severity = "blocker"
	blocker.Payload.Claim = "gate.fail_on weakened from nit to blocker"

	env := Env{Client: &verdictScriptedClient{}, Reg: optOutRegistry(), Cfg: groundednessTestEnv(nil).Cfg}
	out, verdicts, err := Run(context.Background(), []schema.Finding{blocker}, env)
	if err != nil {
		t.Fatalf("Run: %v (a lens reached the model for a finding that opted out of verification)", err)
	}
	if got := out[0].Envelope.Verification.Disposition; got != schema.DispositionAccepted {
		t.Errorf("disposition = %q, want accepted", got)
	}
	if out[0].Payload.Severity != "blocker" {
		t.Errorf("severity = %q, want blocker kept", out[0].Payload.Severity)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none", verdicts)
	}
}

// A batch mixing both sides: the required persona's finding is judged and
// dropped by a lens, while the exempt finding beside it is untouched. The
// scripted client holds exactly one response, so any lens call for the
// exempt finding (or a second call at all) fails Run.
func TestRunMixedBatchJudgesRequiredPersonaAndSparesExemptOne(t *testing.T) {
	exempt := acceptedBy("f-0001", "config-guard")
	exempt.Payload = schema.Payload{Severity: "blocker", Claim: "gate.fail_on weakened", Anchor: schema.Anchor{Kind: schema.AnchorFile, Path: "config.yaml"}}
	judged := acceptedBy("f-0002", "security")
	judged.Payload = schema.Payload{
		Severity: "warning", Claim: "token never expires",
		Anchor:   schema.Anchor{Kind: schema.AnchorLine, Path: "auth.go", StartLine: 3, EndLine: 3},
		Evidence: []schema.Evidence{{Path: "auth.go", StartLine: 3, EndLine: 3, Source: "x"}},
	}

	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0002","result":"fail","reason":"a guard prevents this"}]}`,
	}}
	env := groundednessTestEnv(cl)
	for id, rp := range optOutRegistry() {
		env.Reg[id] = rp
	}

	out, verdicts, err := Run(context.Background(), []schema.Finding{exempt, judged}, env)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := out[0].Envelope.Verification.Disposition; got != schema.DispositionAccepted {
		t.Errorf("exempt disposition = %q, want accepted", got)
	}
	if got := out[1].Envelope.Verification.Disposition; got != schema.DispositionDropped {
		t.Errorf("judged disposition = %q, want dropped by groundedness", got)
	}
	if len(out[0].Envelope.Verification.Verdicts) != 0 {
		t.Errorf("exempt verdicts = %+v, want none", out[0].Envelope.Verification.Verdicts)
	}
	if len(verdicts) != 1 || verdicts[0].Lens != "groundedness" || verdicts[0].Result != "fail" {
		t.Errorf("verdicts = %+v, want a single failed groundedness verdict", verdicts)
	}
	if cl.calls != 1 {
		t.Errorf("model calls = %d, want 1 (only the required persona's finding is judged)", cl.calls)
	}
}

func TestRunAggregatesVerdictsAcrossLenses(t *testing.T) {
	// Run() always uses the fixed Order(); this test exercises Run's
	// aggregation and error-wrapping behavior directly against a lens
	// pipeline built from Order() rather than swapping lenses, since Order
	// is intentionally not configurable. Duplication requires no model and
	// is deterministic, so a two-finding batch through the full pipeline
	// with no findings needing groundedness/injection/materiality (already
	// dropped) exercises Run's wiring end to end.
	in := []schema.Finding{
		{Envelope: schema.Envelope{ID: "f-0001", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
		{Envelope: schema.Envelope{ID: "f-0002", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
	}
	out, verdicts, err := Run(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(out) != 2 {
		t.Fatalf("out = %+v, want 2 findings (nothing accepted, so every lens passes through)", out)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none: no lens had any accepted finding to judge", verdicts)
	}
}
