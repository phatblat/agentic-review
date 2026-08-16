package verify

import (
	"context"
	"fmt"
	"strings"

	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/schema"
)

const materialityPersonaID = "verifier/materiality"

// Materiality is spec §7's materiality lens: a verify-capability judgment
// of whether a finding clears the attention floor. Fail downgrades the
// finding to nit, or drops it when
// cfg.Review.Verification.MaterialityFloor == "drop"; fork PRs force drop
// regardless of config.
type Materiality struct{}

func (Materiality) Name() string { return "materiality" }

func (Materiality) Apply(ctx context.Context, in []schema.Finding, env Env) ([]schema.Finding, []Verdict, error) {
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

	results, err := callVerifyBatch(ctx, env, materialityPersonaID, subjects, renderMaterialityBatch(subjects, env))
	if err != nil {
		return nil, nil, err
	}

	var verdicts []Verdict
	for _, j := range idx {
		f := in[j]
		mv, ok := results[f.Envelope.ID]
		if !ok {
			logMissingVerdict("materiality", f.Envelope.ID)
			continue
		}
		v := Verdict{Lens: "materiality", Result: mv.Result, Checked: "model", Reason: mv.Reason}
		out[j].Envelope.Verification.Verdicts = append(out[j].Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
			Lens: v.Lens, Result: v.Result, Checked: v.Checked, Reason: v.Reason,
		})
		if mv.Result == "fail" {
			if env.IsFork || env.Cfg.Review.Verification.MaterialityFloor == "drop" {
				out[j].Envelope.Verification.Disposition = schema.DispositionDropped
			} else {
				out[j].Envelope.Verification.Disposition = schema.DispositionDowngraded
				out[j].Payload.Severity = "nit"
			}
		}
		verdicts = append(verdicts, v)
	}
	return out, verdicts, nil
}

func renderMaterialityBatch(subjects []schema.Finding, env Env) string {
	var b strings.Builder
	if env.Facts != nil {
		fmt.Fprintf(&b, "PR facts: %d files changed, +%d/-%d lines.\n\n",
			env.Facts.Diff.FilesChanged, env.Facts.Diff.Additions, env.Facts.Diff.Deletions)
	}
	b.WriteString("Findings to judge:\n\n")
	for _, f := range subjects {
		fmt.Fprintf(&b, "finding_id: %s\ncategory: %s\nseverity: %s\n", f.Envelope.ID, f.Payload.Category, f.Payload.Severity)
		b.WriteString(infer.WrapUntrusted("title:"+f.Envelope.ID, f.Payload.Title))
		b.WriteString("\n")
		b.WriteString(infer.WrapUntrusted("claim:"+f.Envelope.ID, f.Payload.Claim))
		b.WriteString("\n---\n")
	}
	return b.String()
}
