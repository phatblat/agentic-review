package gate

import (
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/schema"
)

// Exit implements spec §9's exit-code decision, once tier-2 findings have
// been rendered:
//
//	1  an infra/config failure (a stage error unrelated to the findings
//	   themselves, e.g. an inference endpoint unreachable, config.yaml
//	   failed to load)
//	2  any surviving (disposition == accepted) finding is at or above
//	   cfg.Review.Gate.FailOn, or the diff touches review-config and an
//	   unresolved config-guard blocker survives — this second clause is
//	   deliberately independent of FailOn's value (spec §12.4): a
//	   config-guard blocker fails the gate even if some other bug ever
//	   let a future FailOn misconfiguration bypass the ordinary severity
//	   check, since config-guard's own persona is immutable and its
//	   findings skip verification entirely (verification.required: false)
//	0  otherwise
func Exit(findings []schema.Finding, cfg *config.Config, f *facts.Facts, hadInfraError bool) int {
	if hadInfraError {
		return 1
	}

	threshold := severityRank(cfg.Review.Gate.FailOn)
	triggered := false
	for _, finding := range findings {
		if finding.Envelope.Verification.Disposition != schema.DispositionAccepted {
			continue
		}
		if severityRank(finding.Payload.Severity) >= threshold {
			triggered = true
		}
		if f.Diff.TouchesReviewConfig && finding.Envelope.Persona == "config-guard" && finding.Payload.Severity == "blocker" {
			triggered = true
		}
	}
	if triggered {
		return 2
	}
	return 0
}

func severityRank(s string) int {
	for i, sev := range schema.Severities {
		if sev == s {
			return i
		}
	}
	return -1
}
