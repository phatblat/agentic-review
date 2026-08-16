package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/schema"
)

// groundednessPersonaID is verifier/groundedness's fixed registry id
// (spec §3.3's kind: verifier naming convention).
const groundednessPersonaID = "verifier/groundedness"

// Groundedness is spec §7's groundedness lens: mechanical byte-match
// already ran in internal/runner's M7 stage (a mismatch is dropped there,
// before this lens ever sees the finding); this lens sends the surviving
// claim plus its evidence to the verify capability and asks whether the
// evidence actually supports the claim. Fail drops the finding.
type Groundedness struct{}

func (Groundedness) Name() string { return "groundedness" }

func (Groundedness) Apply(ctx context.Context, in []schema.Finding, env Env) ([]schema.Finding, []Verdict, error) {
	out := make([]schema.Finding, len(in))
	copy(out, in)

	idx := acceptedOnly(in)
	if len(idx) == 0 {
		return out, nil, nil
	}
	subjects := make([]schema.Finding, len(idx))
	for i, j := range idx {
		subjects[i] = in[j]
	}

	results, err := callVerifyBatch(ctx, env, groundednessPersonaID, subjects, renderGroundednessBatch(subjects))
	if err != nil {
		return nil, nil, err
	}

	var verdicts []Verdict
	for _, j := range idx {
		f := in[j]
		mv, ok := results[f.Envelope.ID]
		if !ok {
			logMissingVerdict("groundedness", f.Envelope.ID)
			continue
		}
		v := Verdict{Lens: "groundedness", Result: mv.Result, Checked: "model", Reason: mv.Reason}
		out[j].Envelope.Verification.Verdicts = append(out[j].Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
			Lens: v.Lens, Result: v.Result, Checked: v.Checked, Reason: v.Reason,
		})
		if mv.Result == "fail" {
			out[j].Envelope.Verification.Disposition = schema.DispositionDropped
		}
		verdicts = append(verdicts, v)
	}
	return out, verdicts, nil
}

func renderGroundednessBatch(subjects []schema.Finding) string {
	var b strings.Builder
	b.WriteString("Findings to judge — each already mechanically byte-matches the file content it cites:\n\n")
	for _, f := range subjects {
		fmt.Fprintf(&b, "finding_id: %s\npath: %s\n", f.Envelope.ID, f.Payload.Anchor.Path)
		b.WriteString(infer.WrapUntrusted("claim:"+f.Envelope.ID, f.Payload.Claim))
		b.WriteString("\n")
		for _, e := range f.Payload.Evidence {
			fmt.Fprintf(&b, "evidence at %s:%d-%d\n", e.Path, e.StartLine, e.EndLine)
			b.WriteString(infer.WrapUntrusted("evidence:"+f.Envelope.ID, e.Source))
			b.WriteString("\n")
		}
		b.WriteString("---\n")
	}
	return b.String()
}
