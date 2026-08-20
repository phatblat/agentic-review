// Package roster computes the deterministic tier-2 team (spec §3.4).
//
// The hard ceilings (MaxTeamSize, MaxTotalTokens, ...) that spec §10.2/§12.4
// describe as living in "internal/roster/limits.go" are instead defined in
// internal/persona (as persona.MaxTeamSize etc.): persona.Resolve enforces
// them at config-load time, and Go's import graph must stay acyclic —
// roster already depends on persona for Registry, so persona cannot depend
// back on roster for the same constants. Both packages therefore share the
// persona-owned constants; this file computes the team, it does not define
// the ceilings.
package roster

import (
	"errors"
	"fmt"
	"net/url"
	"sort"

	"github.com/google/cel-go/cel"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// ErrInsufficientForkBudget is returned when, after dropping every
// non-required volunteer, the required-plus-mandatory-fork-guard team
// still exceeds the token budget on a fork PR (spec §12.3): fail closed
// rather than under-review a fork.
var ErrInsufficientForkBudget = errors.New("roster: insufficient budget for fork review")

// Member is one roster.json team entry.
type Member struct {
	ID               string `json:"id"`
	Kind             string `json:"kind"`
	Capability       string `json:"capability,omitempty"`
	ResolvedModel    string `json:"resolved_model,omitempty"`
	ActivationReason string `json:"activation_reason"`
	CompiledSource   string `json:"compiled_source,omitempty"`
	Evaluated        bool   `json:"evaluated"`
	MaxTokens        int    `json:"max_tokens"`
	MaxToolCalls     int    `json:"max_tool_calls"`
}

// Skipped is one non-member registry entry with its skip reason.
type Skipped struct {
	ID     string `json:"id"`
	Reason string `json:"reason"`
}

// Roster is roster.json's shape.
type Roster struct {
	Members     []Member  `json:"members"`
	Verifiers   []string  `json:"verifiers"`
	Skipped     []Skipped `json:"skipped"`
	TotalTokens int       `json:"total_tokens"`
}

// TokenUsage is budget.json's run-level usage block (spec §13.3):
// prompt/cached-prompt/completion/total tokens observed by the live
// infer.Meter across triage, tier-2, and verification. Zero-valued on
// the replay path, which bypasses the live meter.
type TokenUsage struct {
	Prompt       int `json:"prompt"`
	CachedPrompt int `json:"cached_prompt"`
	Completion   int `json:"completion"`
	Total        int `json:"total"`
}

// Budget is spec §13.3's budget.json: allocated vs. consumed tokens per
// persona, plus the run-level Usage block. Owned by roster (not
// internal/runner, which computes it) so internal/artifact can
// serialise it without importing runner, which itself already imports
// both roster and artifact's other domain types.
type Budget struct {
	Allocated map[string]int `json:"allocated"`
	Consumed  map[string]int `json:"consumed"`
	Usage     TokenUsage     `json:"usage"`
}

// candidate is roster.Compute's internal working representation of one
// team member, before serialisation to Member.
type candidate struct {
	rp       *persona.ResolvedPersona
	required bool
	reason   string
	source   string
}

// Compute deterministically selects the tier-2 team in the fixed eight-step
// order from spec §3.4: required, volunteers, excludes, cap, floor,
// escalation, verifiers, budgets.
func Compute(reg persona.Registry, f *facts.Facts, a *schema.Assessment, cfg *config.Config, escalateIDs ...string) (*Roster, error) {
	checkEnv, err := activation.NewEnv(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("roster: build check env: %w", err)
	}

	ids := sortedRegistryIDs(reg)
	required := map[string]*candidate{}
	volunteers := map[string]*candidate{}
	skipped := map[string]string{}

	// Step 1: required.
	for _, id := range ids {
		rp := reg[id]
		if !isTeamKind(rp.Kind) || rp.Required == nil {
			continue
		}
		matched, err := activation.Evaluate(rp.Required, f, a)
		if err != nil {
			return nil, fmt.Errorf("roster: %s: required_when: %w", id, err)
		}
		if matched {
			required[id] = &candidate{rp: rp, required: true, reason: "required_when matched", source: rp.Required.Source}
		} else {
			skipped[id] = "required_when did not match"
		}
	}

	// Step 2: volunteers, for everyone not already required.
	for _, id := range ids {
		if _, ok := required[id]; ok {
			continue
		}
		rp := reg[id]
		if !isTeamKind(rp.Kind) {
			continue
		}
		if !rp.Activation.Always && len(rp.Activation.VolunteerOn) == 0 {
			// Nothing to evaluate here. A required_when that didn't match
			// already left a skip reason in step 1 above — never
			// overwrite it; a persona with neither simply has no rule.
			if _, already := skipped[id]; !already {
				skipped[id] = "no activation rule configured"
			}
			continue
		}
		reason, source, matched, err := evaluateVolunteer(checkEnv, rp, f, a)
		if err != nil {
			return nil, fmt.Errorf("roster: %s: volunteer_on: %w", id, err)
		}
		if matched {
			volunteers[id] = &candidate{rp: rp, reason: reason, source: source}
			delete(skipped, id)
		} else {
			skipped[id] = "volunteer_on did not match"
		}
	}

	// Step 3: excludes. required_when and immutability both beat excludes.
	applyExcludes(required, volunteers, skipped)

	// Step 4: cap.
	team, teamSet := capTeam(required, volunteers, skipped, cfg.Review.Team.Max)

	// Step 5: floor.
	fillFloor(&team, teamSet, skipped, reg, ids, cfg.Review.Team.Min)

	// Step 6: escalation — config rules, then triage's own suggestions,
	// then any persona "escalate" findings-response IDs from a completed
	// tier-2 wave (spec §3.4's third source; empty on the first call,
	// since Compute predates tier-2 output — internal/runner/team.go
	// passes its wave-1 escalate list back in for a second Compute call).
	if err := escalateFromConfig(checkEnv, cfg, f, a, reg, &team, teamSet, skipped); err != nil {
		return nil, err
	}
	escalateFromTriage(a, reg, &team, teamSet, skipped)
	escalateFromWave2(escalateIDs, reg, &team, teamSet, skipped)

	// Step 7: verifiers.
	verifiers := computeVerifierLenses(team, f)

	// Step 8: budgets.
	total, err := enforceBudget(&team, skipped, f)
	if err != nil {
		return nil, err
	}

	return &Roster{
		Members:     buildMembers(team, cfg),
		Verifiers:   verifiers,
		Skipped:     buildSkipped(reg, teamSet, skipped),
		TotalTokens: total,
	}, nil
}

func isTeamKind(k persona.Kind) bool {
	return k == persona.KindAgent || k == persona.KindDeterministic
}

// evaluateVolunteer evaluates rp's volunteer condition, reporting a
// human-readable activation reason and the matched source — the first
// VolunteerOn group to match, in declaration order (1-based in the
// reason), or "always" when Activation.Always is set.
func evaluateVolunteer(checkEnv *cel.Env, rp *persona.ResolvedPersona, f *facts.Facts, a *schema.Assessment) (reason, source string, matched bool, err error) {
	if rp.Activation.Always {
		ok, err := activation.Evaluate(rp.Volunteer, f, a)
		if err != nil {
			return "", "", false, err
		}
		return "always", "true", ok, nil
	}
	for i, group := range rp.Activation.VolunteerOn {
		src, err := activation.Compile(group)
		if err != nil {
			return "", "", false, err
		}
		rule, err := activation.CompileRule(checkEnv, src, activation.ClassFactsAndAssessment, fmt.Sprintf("%s.volunteer_on[%d]", rp.ID, i))
		if err != nil {
			return "", "", false, err
		}
		ok, err := activation.Evaluate(rule, f, a)
		if err != nil {
			return "", "", false, err
		}
		if ok {
			return fmt.Sprintf("volunteer_on group %d matched", i+1), src, true, nil
		}
	}
	return "", "", false, nil
}

func sortedRegistryIDs(reg persona.Registry) []string {
	ids := make([]string, 0, len(reg))
	for id := range reg {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func sortedCandidateIDs(m map[string]*candidate) []string {
	ids := make([]string, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

// ResolvedModel formats "<capability>/<model>@<endpoint-host>" (spec
// §6.2's envelope.model convention), or "" if capability has no
// models[] binding. Exported so internal/runner's envelope stamping uses
// exactly the same format roster.json's resolved_model field does.
func ResolvedModel(cfg *config.Config, capability string) string {
	binding, ok := cfg.Models[capability]
	if !ok {
		return ""
	}
	host := binding.Endpoint
	if u, err := url.Parse(binding.Endpoint); err == nil && u.Host != "" {
		host = u.Host
	}
	return fmt.Sprintf("%s/%s@%s", capability, binding.Model, host)
}
