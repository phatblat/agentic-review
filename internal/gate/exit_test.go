package gate

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/schema"
)

func acceptedFinding(persona, severity string) schema.Finding {
	return schema.Finding{
		Payload:  schema.Payload{Severity: severity},
		Envelope: schema.Envelope{Persona: persona, Verification: schema.Verification{Disposition: schema.DispositionAccepted}},
	}
}

func TestExitInfraErrorReturnsOne(t *testing.T) {
	got := Exit(nil, config.Defaults(), &facts.Facts{}, true)
	if got != 1 {
		t.Errorf("Exit = %d, want 1", got)
	}
}

func TestExitNoFindingsReturnsZero(t *testing.T) {
	got := Exit(nil, config.Defaults(), &facts.Facts{}, false)
	if got != 0 {
		t.Errorf("Exit = %d, want 0", got)
	}
}

func TestExitDefaultFailOnNitFailsOnAnyFinding(t *testing.T) {
	findings := []schema.Finding{acceptedFinding("logic", "nit")}
	got := Exit(findings, config.Defaults(), &facts.Facts{}, false)
	if got != 2 {
		t.Errorf("Exit = %d, want 2 (default fail_on: nit fails on any surviving finding)", got)
	}
}

func TestExitFailOnWarningPassesOnNitOnly(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Gate.FailOn = "warning"
	findings := []schema.Finding{acceptedFinding("logic", "nit")}
	got := Exit(findings, cfg, &facts.Facts{}, false)
	if got != 0 {
		t.Errorf("Exit = %d, want 0: a nit alone must not fail fail_on:warning", got)
	}
}

func TestExitFailOnWarningFailsOnWarning(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Gate.FailOn = "warning"
	findings := []schema.Finding{acceptedFinding("logic", "warning")}
	got := Exit(findings, cfg, &facts.Facts{}, false)
	if got != 2 {
		t.Errorf("Exit = %d, want 2", got)
	}
}

func TestExitIgnoresNonAcceptedDispositions(t *testing.T) {
	dropped := acceptedFinding("logic", "blocker")
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped
	got := Exit([]schema.Finding{dropped}, config.Defaults(), &facts.Facts{}, false)
	if got != 0 {
		t.Errorf("Exit = %d, want 0: a dropped finding must never trigger the gate", got)
	}
}

func TestExitConfigGuardBlockerAlwaysFailsWhenTouchesReviewConfig(t *testing.T) {
	// Even a maximally-weakened fail_on cannot be lower than blocker's own
	// rank in the ordinary check, but this asserts the *independent*
	// config-guard clause fires on its own regardless of FailOn.
	cfg := config.Defaults()
	cfg.Review.Gate.FailOn = "blocker"
	findings := []schema.Finding{acceptedFinding("config-guard", "blocker")}
	f := &facts.Facts{Diff: facts.Diff{TouchesReviewConfig: true}}
	got := Exit(findings, cfg, f, false)
	if got != 2 {
		t.Errorf("Exit = %d, want 2: an unresolved config-guard blocker must always fail the gate", got)
	}
}

func TestExitConfigGuardBlockerIrrelevantWhenNotTouchingReviewConfig(t *testing.T) {
	// A config-guard blocker only exists when review-config was touched
	// (its required_when gates on exactly that fact), but this confirms
	// Exit's own logic keys off TouchesReviewConfig, not persona alone.
	cfg := config.Defaults()
	cfg.Review.Gate.FailOn = "warning"
	dropped := acceptedFinding("config-guard", "blocker")
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped
	f := &facts.Facts{Diff: facts.Diff{TouchesReviewConfig: false}}
	got := Exit([]schema.Finding{dropped}, cfg, f, false)
	if got != 0 {
		t.Errorf("Exit = %d, want 0", got)
	}
}

func TestExitOrderingIndependentOfFindingsSliceOrder(t *testing.T) {
	cfg := config.Defaults()
	cfg.Review.Gate.FailOn = "error"
	findings := []schema.Finding{
		acceptedFinding("logic", "nit"),
		acceptedFinding("logic", "warning"),
		acceptedFinding("security", "error"),
	}
	got := Exit(findings, cfg, &facts.Facts{}, false)
	if got != 2 {
		t.Errorf("Exit = %d, want 2: the error-severity finding must trigger fail_on:error", got)
	}
}
