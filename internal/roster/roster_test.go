package roster

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func testCfg(min, max int) *config.Config {
	cfg := config.Defaults()
	cfg.Review.Team.Min = min
	cfg.Review.Team.Max = max
	cfg.Models = map[string]config.ModelBinding{
		"review": {Endpoint: "http://spark:8000/v1", Model: "qwen3-32b"},
	}
	return cfg
}

func syntheticTriageDef() persona.Definition {
	return persona.Definition{
		ID:      "triage",
		Kind:    persona.KindTriage,
		Summary: "synthetic triage for roster tests",
	}
}

func mustResolve(t *testing.T, defs []persona.Definition, cfg *config.Config) persona.Registry {
	t.Helper()
	reg, err := persona.Resolve(append([]persona.Definition{syntheticTriageDef()}, defs...), nil, cfg)
	if err != nil {
		t.Fatalf("persona.Resolve: %v", err)
	}
	return reg
}

func agentDef(id string, priority int, budget int, activation persona.Activation) persona.Definition {
	return persona.Definition{
		ID:         id,
		Kind:       persona.KindAgent,
		Summary:    id,
		Activation: withPriority(activation, priority),
		Model:      &persona.Model{Capability: "review"},
		Budget:     persona.Budget{MaxTokens: budget, MaxToolCalls: 1},
	}
}

func withPriority(a persona.Activation, p int) persona.Activation {
	a.Priority = p
	return a
}

func TestComputeRequiredAlwaysIncluded(t *testing.T) {
	defs := []persona.Definition{
		agentDef("must-run", 0, 100, persona.Activation{RequiredWhen: "facts.pr.is_fork"}),
		agentDef("never-runs", 0, 100, persona.Activation{RequiredWhen: "facts.pr.draft"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 1))
	f := &facts.Facts{PR: facts.PR{IsFork: true}}
	r, err := Compute(reg, f, nil, testCfg(0, 1))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(r.Members) != 1 || r.Members[0].ID != "must-run" {
		t.Fatalf("Members = %+v, want [must-run]", r.Members)
	}
	if r.Members[0].ActivationReason != "required_when matched" {
		t.Errorf("ActivationReason = %q", r.Members[0].ActivationReason)
	}
}

// Regression: a required_when-only persona (no volunteer_on/always) whose
// required_when does not match must keep its "required_when did not
// match" skip reason — step 2 must not silently overwrite it with "no
// activation rule configured" just because there is nothing to volunteer
// on.
func TestComputeRequiredOnlyMismatchKeepsSkipReason(t *testing.T) {
	defs := []persona.Definition{
		agentDef("filler", 0, 10, persona.Activation{Always: true}),
		agentDef("required-only", 0, 10, persona.Activation{RequiredWhen: "facts.pr.draft"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	var got string
	for _, s := range r.Skipped {
		if s.ID == "required-only" {
			got = s.Reason
		}
	}
	if got != "required_when did not match" {
		t.Errorf("required-only skip reason = %q, want \"required_when did not match\"", got)
	}
}

func TestComputeRequiredExceedsTeamMax(t *testing.T) {
	defs := []persona.Definition{
		agentDef("a", 0, 10, persona.Activation{RequiredWhen: "facts.pr.is_fork"}),
		agentDef("b", 0, 10, persona.Activation{RequiredWhen: "facts.pr.is_fork"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 1))
	f := &facts.Facts{PR: facts.PR{IsFork: true}}
	r, err := Compute(reg, f, nil, testCfg(0, 1))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(r.Members) != 2 {
		t.Fatalf("Members = %+v, want both required members despite team.max=1", r.Members)
	}
}

func TestComputeVolunteerPriorityAndCap(t *testing.T) {
	defs := []persona.Definition{
		agentDef("low", 1, 10, persona.Activation{Always: true}),
		agentDef("high", 10, 10, persona.Activation{Always: true}),
		agentDef("mid", 5, 10, persona.Activation{Always: true}),
	}
	reg := mustResolve(t, defs, testCfg(0, 2))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 2))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if len(ids) != 2 || !contains(ids, "high") || !contains(ids, "mid") {
		t.Fatalf("Members = %v, want [high mid] (by priority, capped at 2)", ids)
	}
	for _, m := range r.Skipped {
		if m.ID == "low" && m.Reason != "team.max reached" {
			t.Errorf("low.Reason = %q, want \"team.max reached\"", m.Reason)
		}
	}
}

func TestComputeVolunteerGroupReasonIndex(t *testing.T) {
	defs := []persona.Definition{
		agentDef("multi", 0, 10, persona.Activation{VolunteerOn: []activation.Trigger{
			{Expr: "false"},
			{Expr: "true"},
		}}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if len(r.Members) != 1 || r.Members[0].ActivationReason != "volunteer_on group 2 matched" {
		t.Fatalf("Members = %+v, want group 2 matched", r.Members)
	}
}

func TestComputeExcludesDropsVolunteer(t *testing.T) {
	defs := []persona.Definition{
		agentDef("owner", 10, 10, persona.Activation{Always: true, Excludes: []string{"victim"}}),
		agentDef("victim", 1, 10, persona.Activation{Always: true}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if contains(ids, "victim") {
		t.Errorf("Members = %v, victim should have been excluded", ids)
	}
}

func TestComputeExcludesCannotDropRequired(t *testing.T) {
	defs := []persona.Definition{
		agentDef("owner", 10, 10, persona.Activation{Always: true, Excludes: []string{"protected"}}),
		agentDef("protected", 1, 10, persona.Activation{RequiredWhen: "true"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if !contains(ids, "protected") {
		t.Errorf("Members = %v, required_when must beat excludes", ids)
	}
}

func TestComputeFloorAddsHighestPriorityRemaining(t *testing.T) {
	defs := []persona.Definition{
		agentDef("required-one", 0, 10, persona.Activation{RequiredWhen: "true"}),
		agentDef("filler-high", 9, 10, persona.Activation{}),
		agentDef("filler-low", 1, 10, persona.Activation{}),
	}
	reg := mustResolve(t, defs, testCfg(3, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(3, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if len(ids) != 3 || !contains(ids, "filler-high") || !contains(ids, "filler-low") {
		t.Fatalf("Members = %v, want all 3 (floor fills from remaining agents)", ids)
	}
}

func TestComputeEscalationFromTriage(t *testing.T) {
	defs := []persona.Definition{
		agentDef("base", 0, 10, persona.Activation{Always: true}),
		agentDef("specialist", 0, 10, persona.Activation{}),
	}
	reg := mustResolve(t, defs, testCfg(0, 1))
	a := &schema.Assessment{SuggestedPersonas: []string{"specialist"}}
	r, err := Compute(reg, &facts.Facts{}, a, testCfg(0, 1))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if !contains(ids, "specialist") {
		t.Fatalf("Members = %v, want specialist escalated in despite team.max=1", ids)
	}
	for _, m := range r.Members {
		if m.ID == "specialist" && m.ActivationReason != "escalation from triage" {
			t.Errorf("specialist.ActivationReason = %q", m.ActivationReason)
		}
	}
}

func TestComputeEscalationFromConfigRule(t *testing.T) {
	defs := []persona.Definition{
		agentDef("base", 0, 10, persona.Activation{Always: true}),
		agentDef("escalatee", 0, 10, persona.Activation{}),
	}
	cfg := testCfg(0, 1)
	cfg.Review.Escalation = []config.Escalation{{When: "facts.pr.is_fork", Add: []string{"escalatee"}}}
	reg := mustResolve(t, defs, cfg)
	f := &facts.Facts{PR: facts.PR{IsFork: true}}
	r, err := Compute(reg, f, nil, cfg)
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if !contains(memberIDs(r), "escalatee") {
		t.Fatalf("Members = %v, want escalatee added by the config escalation rule", memberIDs(r))
	}
}

func TestComputeEscalationFromWave2(t *testing.T) {
	defs := []persona.Definition{
		agentDef("base", 0, 10, persona.Activation{Always: true}),
		agentDef("summoned", 0, 10, persona.Activation{}),
	}
	reg := mustResolve(t, defs, testCfg(0, 1))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 1), "summoned")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if !contains(ids, "summoned") {
		t.Fatalf("Members = %v, want summoned escalated in via the wave-2 escalate-IDs channel despite team.max=1", ids)
	}
	for _, m := range r.Members {
		if m.ID == "summoned" && m.ActivationReason != "escalation from persona escalate output" {
			t.Errorf("summoned.ActivationReason = %q", m.ActivationReason)
		}
	}
}

func TestComputeEscalationFromWave2DeniedAtMaxTeamSize(t *testing.T) {
	var defs []persona.Definition
	for i := 0; i < persona.MaxTeamSize; i++ {
		defs = append(defs, agentDef(idFor(i), 0, 10, persona.Activation{RequiredWhen: "true"}))
	}
	defs = append(defs, agentDef("summoned", 0, 10, persona.Activation{}))
	reg := mustResolve(t, defs, testCfg(0, persona.MaxTeamSize))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, persona.MaxTeamSize), "summoned")
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if contains(memberIDs(r), "summoned") {
		t.Errorf("Members = %v, summoned must be denied at MaxTeamSize even via wave-2 escalation", memberIDs(r))
	}
}

func TestComputeEscalationDeniedAtMaxTeamSize(t *testing.T) {
	var defs []persona.Definition
	for i := 0; i < persona.MaxTeamSize; i++ {
		defs = append(defs, agentDef(idFor(i), 0, 10, persona.Activation{RequiredWhen: "true"}))
	}
	defs = append(defs, agentDef("overflow", 0, 10, persona.Activation{}))
	reg := mustResolve(t, defs, testCfg(0, persona.MaxTeamSize))
	a := &schema.Assessment{SuggestedPersonas: []string{"overflow"}}
	r, err := Compute(reg, &facts.Facts{}, a, testCfg(0, persona.MaxTeamSize))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	if contains(memberIDs(r), "overflow") {
		t.Errorf("Members = %v, overflow must be denied at MaxTeamSize", memberIDs(r))
	}
	if len(r.Members) != persona.MaxTeamSize {
		t.Errorf("len(Members) = %d, want %d", len(r.Members), persona.MaxTeamSize)
	}
}

func TestComputeVerifierLensUnionAndForkInjection(t *testing.T) {
	defs := []persona.Definition{
		{
			ID: "lens-user", Kind: persona.KindAgent, Summary: "x",
			Activation: persona.Activation{Always: true},
			Model:      &persona.Model{Capability: "review"},
			Budget:     persona.Budget{MaxTokens: 10},
			Verification: persona.Verification{
				Required: true,
				Lenses:   []persona.Lens{persona.LensGroundedness, persona.LensMateriality},
			},
		},
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	f := &facts.Facts{PR: facts.PR{IsFork: true}}
	r, err := Compute(reg, f, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	want := []string{"groundedness", "injection", "materiality"}
	if !equalStrings(r.Verifiers, want) {
		t.Errorf("Verifiers = %v, want %v", r.Verifiers, want)
	}
}

func TestComputeBudgetDropsLowestPriorityVolunteer(t *testing.T) {
	defs := []persona.Definition{
		agentDef("required", 0, 100, persona.Activation{RequiredWhen: "true"}),
		agentDef("cheap-high-priority", 10, 50, persona.Activation{Always: true}),
		agentDef("expensive-low-priority", 1, persona.MaxTotalTokens, persona.Activation{Always: true}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v", err)
	}
	ids := memberIDs(r)
	if contains(ids, "expensive-low-priority") {
		t.Errorf("Members = %v, expensive-low-priority should have been dropped to fit budget", ids)
	}
	if !contains(ids, "required") || !contains(ids, "cheap-high-priority") {
		t.Errorf("Members = %v, want required and cheap-high-priority kept", ids)
	}
}

func TestComputeBudgetForkFailsClosed(t *testing.T) {
	defs := []persona.Definition{
		agentDef("required-a", 0, persona.MaxTotalTokens, persona.Activation{RequiredWhen: "true"}),
		agentDef("required-b", 0, persona.MaxTotalTokens, persona.Activation{RequiredWhen: "true"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	f := &facts.Facts{PR: facts.PR{IsFork: true}}
	_, err := Compute(reg, f, nil, testCfg(0, 5))
	if err == nil {
		t.Fatalf("Compute succeeded, want ErrInsufficientForkBudget")
	}
}

func TestComputeBudgetNonForkProceedsOverBudget(t *testing.T) {
	defs := []persona.Definition{
		agentDef("required-a", 0, persona.MaxTotalTokens, persona.Activation{RequiredWhen: "true"}),
		agentDef("required-b", 0, persona.MaxTotalTokens, persona.Activation{RequiredWhen: "true"}),
	}
	reg := mustResolve(t, defs, testCfg(0, 5))
	r, err := Compute(reg, &facts.Facts{}, nil, testCfg(0, 5))
	if err != nil {
		t.Fatalf("Compute: %v, want a warned-but-successful over-budget result on a non-fork PR", err)
	}
	if len(r.Members) != 2 {
		t.Errorf("Members = %+v, want both required members kept", r.Members)
	}
}

func memberIDs(r *Roster) []string {
	ids := make([]string, len(r.Members))
	for i, m := range r.Members {
		ids[i] = m.ID
	}
	return ids
}

func contains(ids []string, id string) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func idFor(i int) string {
	return string(rune('a' + i))
}
