package verify

import (
	"context"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func materialityTestEnv(cl infer.Client, isFork bool, floor string) Env {
	cfg := config.Defaults()
	cfg.Review.Verification.MaterialityFloor = floor
	cfg.Models = map[string]config.ModelBinding{"verify": {Endpoint: "http://spark/v1", Model: "qwen3-8b"}}
	reg := persona.Registry{
		materialityPersonaID: {Definition: persona.Definition{
			ID: materialityPersonaID, Kind: persona.KindVerifier, Lens: persona.LensMateriality,
			Model:  &persona.Model{Capability: "verify"},
			Budget: persona.Budget{MaxTokens: 2000},
		}},
	}
	return Env{
		Client: cl, Cfg: cfg, Reg: reg, IsFork: isFork,
		Prompts: map[string]string{materialityPersonaID: "judge materiality"},
		Facts:   &facts.Facts{Diff: facts.Diff{FilesChanged: 3, Additions: 10, Deletions: 2}},
	}
}

func materialityFinding(id, severity string) schema.Finding {
	return schema.Finding{
		Payload: schema.Payload{
			Category: "style", Severity: severity, Title: "t", Claim: "minor nit", Confidence: 0.5,
			Anchor: schema.Anchor{Kind: schema.AnchorPR},
		},
		Envelope: schema.Envelope{ID: id, Verification: schema.Verification{Disposition: schema.DispositionAccepted}},
	}
}

func TestMaterialityPassKeepsSeverity(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"pass","reason":"proportionate"}]}`,
	}}
	in := []schema.Finding{materialityFinding("f-0001", "warning")}

	out, _, err := Materiality{}.Apply(context.Background(), in, materialityTestEnv(cl, false, "downgrade"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionAccepted || out[0].Payload.Severity != "warning" {
		t.Errorf("out[0] = %+v, want accepted at its original severity", out[0])
	}
}

func TestMaterialityFailDowngradesToNitByDefault(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"fail","reason":"too minor to act on"}]}`,
	}}
	in := []schema.Finding{materialityFinding("f-0001", "warning")}

	out, _, err := Materiality{}.Apply(context.Background(), in, materialityTestEnv(cl, false, "downgrade"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDowngraded {
		t.Errorf("disposition = %q, want downgraded", out[0].Envelope.Verification.Disposition)
	}
	if out[0].Payload.Severity != "nit" {
		t.Errorf("severity = %q, want nit", out[0].Payload.Severity)
	}
	if len(out[0].Envelope.Verification.Verdicts) != 1 || out[0].Envelope.Verification.Verdicts[0].Reason != "too minor to act on" {
		t.Errorf("stamped verdict = %+v, want the model's reason copied through", out[0].Envelope.Verification.Verdicts)
	}
}

func TestMaterialityFailDropsWhenConfigFloorIsDrop(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"fail","reason":"too minor"}]}`,
	}}
	in := []schema.Finding{materialityFinding("f-0001", "warning")}

	out, _, err := Materiality{}.Apply(context.Background(), in, materialityTestEnv(cl, false, "drop"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("disposition = %q, want dropped (materiality_floor: drop)", out[0].Envelope.Verification.Disposition)
	}
}

func TestMaterialityFailDropsOnForkRegardlessOfConfig(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"fail","reason":"too minor"}]}`,
	}}
	in := []schema.Finding{materialityFinding("f-0001", "warning")}

	// materiality_floor is "downgrade" (the non-fork default), but fork
	// PRs must still force a drop (spec §7).
	out, _, err := Materiality{}.Apply(context.Background(), in, materialityTestEnv(cl, true, "downgrade"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("disposition = %q, want dropped: fork PRs force drop regardless of config", out[0].Envelope.Verification.Disposition)
	}
	if out[0].Payload.Severity != "warning" {
		t.Errorf("severity = %q, want left unchanged (dropped, not downgraded)", out[0].Payload.Severity)
	}
}

func TestMaterialitySkipsAlreadyDroppedFindings(t *testing.T) {
	cl := &verdictScriptedClient{}
	dropped := materialityFinding("f-0001", "warning")
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped

	_, verdicts, err := Materiality{}.Apply(context.Background(), []schema.Finding{dropped}, materialityTestEnv(cl, false, "downgrade"))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none", verdicts)
	}
	if cl.calls != 0 {
		t.Errorf("calls = %d, want 0", cl.calls)
	}
}
