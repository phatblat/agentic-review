package verify

import (
	"context"
	"testing"

	"github.com/phatblat/agentic-review/internal/schema"
)

func dupFinding(id, fingerprint, persona, path string, startLine, endLine int, claim, severity string, confidence float64) schema.Finding {
	return schema.Finding{
		Payload: schema.Payload{
			Category: "correctness", Severity: severity, Title: "t", Claim: claim, Confidence: confidence,
			Anchor: schema.Anchor{Kind: schema.AnchorLine, Path: path, StartLine: startLine, EndLine: endLine},
		},
		Envelope: schema.Envelope{
			ID: id, Fingerprint: fingerprint, Persona: persona,
			Verification: schema.Verification{Disposition: schema.DispositionAccepted},
		},
	}
}

func TestDuplicationMergesEqualFingerprints(t *testing.T) {
	in := []schema.Finding{
		dupFinding("f-0001", "sha256:same", "logic", "a.go", 1, 1, "claim one", "warning", 0.5),
		dupFinding("f-0002", "sha256:same", "security", "b.go", 99, 99, "totally different claim text", "error", 0.5),
	}
	out, verdicts, err := Duplication{}.Apply(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want exactly 1 (one loser)", verdicts)
	}
	// error > warning, so f-0002 survives.
	if out[0].Envelope.Verification.Disposition != schema.DispositionMerged {
		t.Errorf("f-0001 disposition = %q, want merged", out[0].Envelope.Verification.Disposition)
	}
	if out[1].Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("f-0002 disposition = %q, want accepted (higher severity survives)", out[1].Envelope.Verification.Disposition)
	}
	if len(out[1].Envelope.MergedPersonas) != 2 {
		t.Errorf("MergedPersonas = %v, want both logic and security credited", out[1].Envelope.MergedPersonas)
	}
	if len(out[0].Envelope.Verification.Verdicts) != 1 || out[0].Envelope.Verification.Verdicts[0].Reason == "" {
		t.Errorf("loser's stamped verdict = %+v, want a non-empty reason", out[0].Envelope.Verification.Verdicts)
	}
}

func TestDuplicationMergesEqualAnchorAndClaimStem(t *testing.T) {
	in := []schema.Finding{
		dupFinding("f-0001", "sha256:aaa", "logic", "a.go", 10, 20, "this function leaks a database connection on error", "warning", 0.5),
		dupFinding("f-0002", "sha256:bbb", "security", "a.go", 10, 20, "this function leaks a database connection on error!!", "warning", 0.5),
	}
	out, _, err := Duplication{}.Apply(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	mergedCount := 0
	for _, f := range out {
		if f.Envelope.Verification.Disposition == schema.DispositionMerged {
			mergedCount++
		}
	}
	if mergedCount != 1 {
		t.Fatalf("out = %+v, want exactly one loser (same anchor, same claim stem after normalisation)", out)
	}
}

func TestDuplicationDifferentAnchorAndFingerprintNeverMerges(t *testing.T) {
	in := []schema.Finding{
		dupFinding("f-0001", "sha256:aaa", "logic", "a.go", 1, 1, "claim one", "warning", 0.5),
		dupFinding("f-0002", "sha256:bbb", "security", "b.go", 2, 2, "an entirely unrelated claim", "warning", 0.5),
	}
	out, verdicts, err := Duplication{}.Apply(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 0 {
		t.Fatalf("verdicts = %+v, want none: nothing should merge", verdicts)
	}
	for _, f := range out {
		if f.Envelope.Verification.Disposition != schema.DispositionAccepted {
			t.Errorf("disposition = %q, want accepted for an unrelated finding", f.Envelope.Verification.Disposition)
		}
	}
}

func TestDuplicationSurvivorTieBreaksOnConfidenceThenID(t *testing.T) {
	in := []schema.Finding{
		dupFinding("f-0003", "sha256:same", "c", "a.go", 1, 1, "claim", "warning", 0.9), // highest confidence
		dupFinding("f-0001", "sha256:same", "a", "a.go", 1, 1, "claim", "warning", 0.5),
		dupFinding("f-0002", "sha256:same", "b", "a.go", 1, 1, "claim", "warning", 0.5),
	}
	out, _, err := Duplication{}.Apply(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("f-0003 (highest confidence) disposition = %q, want accepted", out[0].Envelope.Verification.Disposition)
	}
	if out[1].Envelope.Verification.Disposition != schema.DispositionMerged || out[2].Envelope.Verification.Disposition != schema.DispositionMerged {
		t.Errorf("out = %+v, want the two lower-confidence findings merged", out)
	}
}

func TestDuplicationTransitiveChainMergesIntoOneGroup(t *testing.T) {
	// f-0001 and f-0002 share a fingerprint; f-0002 and f-0003 share an
	// anchor+claim-stem. All three must end up in one group even though
	// f-0001 and f-0003 match neither condition directly.
	in := []schema.Finding{
		dupFinding("f-0001", "sha256:same", "a", "a.go", 1, 1, "claim alpha", "warning", 0.5),
		dupFinding("f-0002", "sha256:same", "b", "b.go", 5, 5, "claim beta", "error", 0.5),
		dupFinding("f-0003", "sha256:other", "c", "b.go", 5, 5, "claim beta", "warning", 0.5),
	}
	out, verdicts, err := Duplication{}.Apply(context.Background(), in, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("verdicts = %+v, want 2 losers in one 3-member group", verdicts)
	}
	survivors := 0
	for _, f := range out {
		if f.Envelope.Verification.Disposition == schema.DispositionAccepted {
			survivors++
		}
	}
	if survivors != 1 {
		t.Fatalf("out = %+v, want exactly 1 survivor across the transitively-linked group", out)
	}
}

func TestDuplicationSkipsAlreadyDroppedFindings(t *testing.T) {
	dropped := dupFinding("f-0001", "sha256:same", "a", "a.go", 1, 1, "claim", "warning", 0.5)
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped
	accepted := dupFinding("f-0002", "sha256:same", "b", "a.go", 1, 1, "claim", "warning", 0.5)

	out, verdicts, err := Duplication{}.Apply(context.Background(), []schema.Finding{dropped, accepted}, Env{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(verdicts) != 0 {
		t.Errorf("verdicts = %+v, want none: only one candidate was still accepted, nothing to merge with", verdicts)
	}
	if out[1].Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("accepted finding's disposition changed to %q", out[1].Envelope.Verification.Disposition)
	}
}
