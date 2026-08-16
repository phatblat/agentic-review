package verify

import (
	"context"
	"errors"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// verdictScriptedClient returns one scripted verdicts.v1 JSON response per
// call, matching the retry/tool-loop shape internal/runner's
// scriptedClient exercises for findings.v1.
type verdictScriptedClient struct {
	responses []string
	errs      []error
	calls     int
}

func (c *verdictScriptedClient) Complete(_ context.Context, _ string, _ *infer.Request) (*infer.Response, error) {
	i := c.calls
	c.calls++
	if i < len(c.errs) && c.errs[i] != nil {
		return nil, c.errs[i]
	}
	if i >= len(c.responses) {
		return nil, errors.New("verdictScriptedClient: out of responses")
	}
	return &infer.Response{Choices: []infer.Choice{{Message: infer.Message{Role: "assistant", Content: c.responses[i]}}}}, nil
}

func groundednessTestEnv(cl infer.Client) Env {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{"verify": {Endpoint: "http://spark/v1", Model: "qwen3-8b"}}
	reg := persona.Registry{
		groundednessPersonaID: {Definition: persona.Definition{
			ID: groundednessPersonaID, Kind: persona.KindVerifier, Lens: persona.LensGroundedness,
			Model:  &persona.Model{Capability: "verify"},
			Budget: persona.Budget{MaxTokens: 2000},
		}},
	}
	return Env{Client: cl, Cfg: cfg, Reg: reg, Prompts: map[string]string{groundednessPersonaID: "judge groundedness"}}
}

func acceptedFinding(id, path string, startLine, endLine int, claim string) schema.Finding {
	return schema.Finding{
		Payload: schema.Payload{
			Category: "correctness", Severity: "warning", Title: "t", Claim: claim, Confidence: 0.7,
			Anchor:   schema.Anchor{Kind: schema.AnchorLine, Path: path, StartLine: startLine, EndLine: endLine},
			Evidence: []schema.Evidence{{Path: path, StartLine: startLine, EndLine: endLine, Source: "code"}},
		},
		Envelope: schema.Envelope{ID: id, Verification: schema.Verification{Disposition: schema.DispositionAccepted}},
	}
}

func TestGroundednessPassSurvives(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"pass","reason":"evidence demonstrates the claim"}]}`,
	}}
	in := []schema.Finding{acceptedFinding("f-0001", "f.go", 1, 1, "claim")}

	out, verdicts, err := Groundedness{}.Apply(context.Background(), in, groundednessTestEnv(cl))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("disposition = %q, want accepted", out[0].Envelope.Verification.Disposition)
	}
	if len(verdicts) != 1 || verdicts[0].Result != "pass" || verdicts[0].Checked != "model" {
		t.Fatalf("verdicts = %+v", verdicts)
	}
}

func TestGroundednessFailDrops(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{
		`{"verdicts":[{"finding_id":"f-0001","result":"fail","reason":"a guard prevents this"}]}`,
	}}
	in := []schema.Finding{acceptedFinding("f-0001", "f.go", 1, 1, "claim")}

	out, _, err := Groundedness{}.Apply(context.Background(), in, groundednessTestEnv(cl))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("disposition = %q, want dropped", out[0].Envelope.Verification.Disposition)
	}
	found := false
	for _, v := range out[0].Envelope.Verification.Verdicts {
		if v.Lens == "groundedness" && v.Result == "fail" {
			found = true
			if v.Reason != "a guard prevents this" {
				t.Errorf("Reason = %q, want the model's reason copied through", v.Reason)
			}
		}
	}
	if !found {
		t.Errorf("verdicts = %+v, want a failed groundedness verdict recorded", out[0].Envelope.Verification.Verdicts)
	}
}

func TestGroundednessSkipsAlreadyDroppedFindings(t *testing.T) {
	cl := &verdictScriptedClient{} // no responses scripted: a call here is a test bug
	dropped := acceptedFinding("f-0001", "f.go", 1, 1, "claim")
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped

	out, verdicts, err := Groundedness{}.Apply(context.Background(), []schema.Finding{dropped}, groundednessTestEnv(cl))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none: the finding was already dropped before this lens ran", verdicts)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("disposition changed for an already-dropped finding: %q", out[0].Envelope.Verification.Disposition)
	}
	if cl.calls != 0 {
		t.Errorf("calls = %d, want 0: no model call for a finding this lens should skip", cl.calls)
	}
}

func TestGroundednessMissingVerdictLeavesFindingUnjudged(t *testing.T) {
	cl := &verdictScriptedClient{responses: []string{`{"verdicts":[]}`}}
	in := []schema.Finding{acceptedFinding("f-0001", "f.go", 1, 1, "claim")}

	out, verdicts, err := Groundedness{}.Apply(context.Background(), in, groundednessTestEnv(cl))
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none: the model omitted this finding_id", verdicts)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("disposition = %q, want left as accepted when the model omits a verdict", out[0].Envelope.Verification.Disposition)
	}
}

func TestGroundednessModelErrorPropagates(t *testing.T) {
	cl := &verdictScriptedClient{errs: []error{errors.New("endpoint unreachable")}}
	in := []schema.Finding{acceptedFinding("f-0001", "f.go", 1, 1, "claim")}

	lens := Groundedness{}
	if _, _, err := lens.Apply(context.Background(), in, groundednessTestEnv(cl)); err == nil {
		t.Fatalf("Apply succeeded despite a model call error, want it to fail closed")
	}
}
