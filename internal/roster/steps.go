package roster

import (
	"fmt"
	"sort"

	"github.com/google/cel-go/cel"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// applyExcludes drops, from volunteers, every persona any required-or-
// volunteer member's Excludes lists — except a persona in required or
// marked immutable, which required_when/immutability beat excludes for
// (spec §3.3). Iterates in sorted ID order for determinism.
func applyExcludes(required, volunteers map[string]*candidate, skipped map[string]string) {
	union := make(map[string]*candidate, len(required)+len(volunteers))
	for id, c := range required {
		union[id] = c
	}
	for id, c := range volunteers {
		union[id] = c
	}
	for _, id := range sortedCandidateIDs(union) {
		c, ok := union[id]
		if !ok {
			continue // already dropped by an earlier member's excludes
		}
		for _, excludedID := range c.rp.Activation.Excludes {
			if _, isRequired := required[excludedID]; isRequired {
				continue
			}
			target, present := union[excludedID]
			if !present {
				continue
			}
			if target.rp.Immutable {
				continue
			}
			delete(union, excludedID)
			delete(volunteers, excludedID)
			skipped[excludedID] = fmt.Sprintf("excluded by %s", id)
		}
	}
}

// capTeam builds the team: every required member, then volunteers sorted
// by (priority desc, id asc) until teamMax. Required members always all
// run even past teamMax.
func capTeam(required, volunteers map[string]*candidate, skipped map[string]string, teamMax int) ([]*candidate, map[string]bool) {
	var team []*candidate
	teamSet := map[string]bool{}
	for _, id := range sortedCandidateIDs(required) {
		team = append(team, required[id])
		teamSet[id] = true
	}
	if len(team) > teamMax {
		logx.Warn("roster: required team size %d exceeds team.max %d; every required persona still runs", len(team), teamMax)
	}

	volunteerIDs := sortedCandidateIDs(volunteers)
	sort.Slice(volunteerIDs, func(i, j int) bool {
		pi := volunteers[volunteerIDs[i]].rp.Activation.Priority
		pj := volunteers[volunteerIDs[j]].rp.Activation.Priority
		if pi != pj {
			return pi > pj // priority desc
		}
		return volunteerIDs[i] < volunteerIDs[j] // id asc
	})
	for _, id := range volunteerIDs {
		if len(team) >= teamMax {
			skipped[id] = "team.max reached"
			continue
		}
		team = append(team, volunteers[id])
		teamSet[id] = true
	}
	return team, teamSet
}

// fillFloor adds the highest-priority remaining enabled agent personas
// until team.min is met, or none remain.
func fillFloor(team *[]*candidate, teamSet map[string]bool, skipped map[string]string, reg persona.Registry, ids []string, teamMin int) {
	if len(*team) >= teamMin {
		return
	}
	var pool []string
	for _, id := range ids {
		if teamSet[id] || reg[id].Kind != persona.KindAgent {
			continue
		}
		pool = append(pool, id)
	}
	sort.Slice(pool, func(i, j int) bool {
		pi, pj := reg[pool[i]].Activation.Priority, reg[pool[j]].Activation.Priority
		if pi != pj {
			return pi > pj
		}
		return pool[i] < pool[j]
	})
	for _, id := range pool {
		if len(*team) >= teamMin {
			break
		}
		rp := reg[id]
		*team = append(*team, &candidate{rp: rp, reason: "floor: added to meet team.min"})
		teamSet[id] = true
		delete(skipped, id)
	}
	if len(*team) < teamMin {
		logx.Warn("roster: team size %d is below team.min %d and no more enabled agent personas remain", len(*team), teamMin)
	}
}

// escalateFromConfig evaluates cfg.Review.Escalation[].when and, for each
// matching rule, tries to add every persona id in its Add list.
func escalateFromConfig(checkEnv *cel.Env, cfg *config.Config, f *facts.Facts, a *schema.Assessment, reg persona.Registry, team *[]*candidate, teamSet map[string]bool, skipped map[string]string) error {
	for i, esc := range cfg.Review.Escalation {
		rule, err := activation.CompileRule(checkEnv, esc.When, activation.ClassFactsAndAssessment, fmt.Sprintf("escalation[%d].when", i))
		if err != nil {
			return fmt.Errorf("roster: %w", err)
		}
		matched, err := activation.Evaluate(rule, f, a)
		if err != nil {
			return fmt.Errorf("roster: evaluate escalation[%d].when: %w", i, err)
		}
		if !matched {
			continue
		}
		for _, id := range esc.Add {
			tryEscalate(reg, team, teamSet, skipped, id, fmt.Sprintf("escalation rule %d matched", i))
		}
	}
	return nil
}

// escalateFromTriage tries to add every persona triage's assessment
// advisorily suggested.
func escalateFromTriage(a *schema.Assessment, reg persona.Registry, team *[]*candidate, teamSet map[string]bool, skipped map[string]string) {
	if a == nil {
		return
	}
	for _, id := range a.SuggestedPersonas {
		tryEscalate(reg, team, teamSet, skipped, id, "escalation from triage")
	}
}

// escalateFromWave2 tries to add every persona id a tier-2 wave's agents
// requested via their findings-response "escalate" field (spec §3.4's
// third escalation source).
func escalateFromWave2(escalateIDs []string, reg persona.Registry, team *[]*candidate, teamSet map[string]bool, skipped map[string]string) {
	for _, id := range escalateIDs {
		tryEscalate(reg, team, teamSet, skipped, id, "escalation from persona escalate output")
	}
}

// tryEscalate adds id to team if it is registered, not already a member,
// and the team is under the hard MaxTeamSize ceiling.
func tryEscalate(reg persona.Registry, team *[]*candidate, teamSet map[string]bool, skipped map[string]string, id, reason string) {
	if teamSet[id] {
		return
	}
	rp, ok := reg[id]
	if !ok {
		logx.Warn("escalation: denied %s (not in registry)", id)
		return
	}
	if len(*team) >= persona.MaxTeamSize {
		logx.Warn("escalation: denied %s (team budget exhausted)", id)
		return
	}
	*team = append(*team, &candidate{rp: rp, reason: reason})
	teamSet[id] = true
	delete(skipped, id)
	logx.Warn("escalation: added %s (%s)", id, reason)
}

// computeVerifierLenses unions verification.lenses across team members
// with verification.required: true, plus injection unconditionally on
// fork PRs when the team has any agent member (spec §12.3).
func computeVerifierLenses(team []*candidate, f *facts.Facts) []string {
	lensSet := map[string]bool{}
	hasAgent := false
	for _, c := range team {
		if c.rp.Kind == persona.KindAgent {
			hasAgent = true
		}
		if c.rp.Verification.Required {
			for _, l := range c.rp.Verification.Lenses {
				lensSet[string(l)] = true
			}
		}
	}
	if f.PR.IsFork && hasAgent {
		lensSet["injection"] = true
	}
	verifiers := make([]string, 0, len(lensSet))
	for l := range lensSet {
		verifiers = append(verifiers, l)
	}
	sort.Strings(verifiers)
	return verifiers
}

// enforceBudget drops the lowest-priority non-required volunteers, in
// ascending priority order, until the team's total max_tokens fits
// persona.MaxTotalTokens. If it still doesn't fit with only required
// members left and the PR is a fork, it fails closed
// (ErrInsufficientForkBudget); otherwise it proceeds over budget with a
// warning.
func enforceBudget(team *[]*candidate, skipped map[string]string, f *facts.Facts) (int, error) {
	total := sumTokens(*team)
	if total <= persona.MaxTotalTokens {
		return total, nil
	}

	droppable := make([]*candidate, 0, len(*team))
	for _, c := range *team {
		if !c.required {
			droppable = append(droppable, c)
		}
	}
	sort.Slice(droppable, func(i, j int) bool {
		pi, pj := droppable[i].rp.Activation.Priority, droppable[j].rp.Activation.Priority
		if pi != pj {
			return pi < pj // ascending: lowest priority dropped first
		}
		return droppable[i].rp.ID > droppable[j].rp.ID
	})

	dropped := map[string]bool{}
	for _, c := range droppable {
		if total <= persona.MaxTotalTokens {
			break
		}
		dropped[c.rp.ID] = true
		total -= c.rp.Budget.MaxTokens
		skipped[c.rp.ID] = "dropped: token budget exceeded"
		logx.Warn("roster: dropping %s to fit MaxTotalTokens", c.rp.ID)
	}
	if len(dropped) > 0 {
		filtered := (*team)[:0]
		for _, c := range *team {
			if !dropped[c.rp.ID] {
				filtered = append(filtered, c)
			}
		}
		*team = filtered
	}

	if total > persona.MaxTotalTokens {
		if f.PR.IsFork {
			return total, ErrInsufficientForkBudget
		}
		logx.Warn("roster: team token budget %d still exceeds MaxTotalTokens %d after dropping every non-required volunteer", total, persona.MaxTotalTokens)
	}
	return total, nil
}

func sumTokens(team []*candidate) int {
	total := 0
	for _, c := range team {
		total += c.rp.Budget.MaxTokens
	}
	return total
}

func buildMembers(team []*candidate, cfg *config.Config) []Member {
	members := make([]Member, 0, len(team))
	for _, c := range team {
		m := Member{
			ID:               c.rp.ID,
			Kind:             string(c.rp.Kind),
			ActivationReason: c.reason,
			CompiledSource:   c.source,
			Evaluated:        true,
			MaxTokens:        c.rp.Budget.MaxTokens,
			MaxToolCalls:     c.rp.Budget.MaxToolCalls,
		}
		if c.rp.Model != nil {
			m.Capability = c.rp.Model.Capability
			m.ResolvedModel = ResolvedModel(cfg, c.rp.Model.Capability)
		}
		members = append(members, m)
	}
	sort.Slice(members, func(i, j int) bool { return members[i].ID < members[j].ID })
	return members
}

func buildSkipped(reg persona.Registry, teamSet map[string]bool, skipped map[string]string) []Skipped {
	out := make([]Skipped, 0, len(skipped))
	for id, rp := range reg {
		if teamSet[id] || !isTeamKind(rp.Kind) {
			continue
		}
		reason, ok := skipped[id]
		if !ok {
			reason = "not selected"
		}
		out = append(out, Skipped{ID: id, Reason: reason})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
