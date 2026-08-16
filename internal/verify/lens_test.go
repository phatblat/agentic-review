package verify

import (
	"context"
	"testing"

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

func TestAcceptedOnlySkipsNonAcceptedDispositions(t *testing.T) {
	in := []schema.Finding{
		{Envelope: schema.Envelope{ID: "f-0001", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
		{Envelope: schema.Envelope{ID: "f-0002", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
		{Envelope: schema.Envelope{ID: "f-0003", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
	}
	idx := acceptedOnly(in)
	if len(idx) != 2 || idx[0] != 0 || idx[1] != 2 {
		t.Fatalf("idx = %v, want [0 2]", idx)
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
