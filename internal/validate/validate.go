// Package validate exhaustively compiles and context-class lints every
// CEL activation rule reachable from a resolved registry and config —
// every persona's volunteer_on groups (required_when is already
// compiled and lint-checked by persona.Resolve itself), every config
// escalation[].when, and every config skip_when entry — regardless of
// whether roster/gate evaluation would short-circuit past some of them
// at runtime. Shared by `agentic-review validate` and the review
// entrypoint (spec §5.1: "the same code path the runtime uses at load
// time, not a parallel implementation").
package validate

import (
	"fmt"
	"sort"

	"github.com/google/cel-go/cel"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/persona"
)

// RuleCheck is one CEL rule's exhaustive compile+lint result.
type RuleCheck struct {
	PersonaID string // "" for a config-level rule (escalation, skip_when)
	Slot      string // e.g. "volunteer_on[0]", "escalation[0].when", "skip_when[0]"
	Allowed   activation.ContextClass
	Source    string // the compiled CEL source; "" when compilation itself failed
	Err       error  // nil on success
}

// isTeamKind reports whether k is ever selected as a tier-2 team member
// (agent or deterministic) — the only kinds whose volunteer_on groups
// roster.Compute ever evaluates.
func isTeamKind(k persona.Kind) bool {
	return k == persona.KindAgent || k == persona.KindDeterministic
}

// All exhaustively compiles and lints every rule reachable from reg and
// cfg. It returns one RuleCheck per rule in a stable order (personas
// sorted by id, then declaration order within a persona; config
// escalation and skip_when in declaration order after every persona),
// regardless of any individual rule's outcome — callers decide whether
// any error fails the run.
func All(reg persona.Registry, cfg *config.Config) ([]RuleCheck, error) {
	checkEnv, err := activation.NewEnv(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("validate: build check env: %w", err)
	}

	var out []RuleCheck
	for _, id := range sortedIDs(reg) {
		rp := reg[id]
		if !isTeamKind(rp.Kind) {
			continue
		}
		for i, group := range rp.Activation.VolunteerOn {
			slot := fmt.Sprintf("%s.volunteer_on[%d]", id, i)
			out = append(out, checkTrigger(checkEnv, group, activation.ClassFactsAndAssessment, id, slot))
		}
	}
	for i, esc := range cfg.Review.Escalation {
		slot := fmt.Sprintf("escalation[%d].when", i)
		out = append(out, checkSource(checkEnv, esc.When, activation.ClassFactsAndAssessment, "", slot))
	}
	for i, source := range cfg.Review.SkipWhen {
		slot := fmt.Sprintf("skip_when[%d]", i)
		out = append(out, checkSource(checkEnv, source, activation.ClassFactsOnly, "", slot))
	}
	return out, nil
}

// checkTrigger sugar-compiles a volunteer_on trigger group to CEL source,
// then compile+lints it.
func checkTrigger(checkEnv *cel.Env, group activation.Trigger, allowed activation.ContextClass, personaID, slot string) RuleCheck {
	source, err := activation.Compile(group)
	if err != nil {
		return RuleCheck{PersonaID: personaID, Slot: slot, Allowed: allowed, Err: fmt.Errorf("activation: %s: %w", slot, err)}
	}
	return checkSource(checkEnv, source, allowed, personaID, slot)
}

// checkSource compile+lints an already-CEL-shaped source string (a
// config-level rule, or a trigger group's sugar-compiled output).
func checkSource(checkEnv *cel.Env, source string, allowed activation.ContextClass, personaID, slot string) RuleCheck {
	check := RuleCheck{PersonaID: personaID, Slot: slot, Allowed: allowed, Source: source}
	if _, err := activation.CompileRule(checkEnv, source, allowed, slot); err != nil {
		check.Err = err
	}
	return check
}

func sortedIDs(reg persona.Registry) []string {
	ids := make([]string, 0, len(reg))
	for id := range reg {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
