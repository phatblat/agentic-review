package roster

import (
	"strings"
	"testing"
)

func TestStepSummaryTableIncludesHeadingMembersAndSkipped(t *testing.T) {
	r := &Roster{
		Members:     []Member{{ID: "security", Kind: "agent", ResolvedModel: "review/qwen3-32b@spark:8000", ActivationReason: "required_when matched", MaxTokens: 12000}},
		Verifiers:   []string{"groundedness", "injection"},
		Skipped:     []Skipped{{ID: "logic", Reason: "volunteer_on did not match"}},
		TotalTokens: 12000,
	}
	got := r.StepSummaryTable("## agentic-review")

	if !strings.HasPrefix(got, "## agentic-review\n") {
		t.Errorf("got = %q, want it to start with the given heading", got)
	}
	if !strings.Contains(got, "security") || !strings.Contains(got, "required_when matched") {
		t.Errorf("got = %q, want the member row present", got)
	}
	if !strings.Contains(got, "verifier lenses: groundedness, injection") {
		t.Errorf("got = %q, want the verifier lenses line", got)
	}
	if !strings.Contains(got, "### Skipped") || !strings.Contains(got, "volunteer_on did not match") {
		t.Errorf("got = %q, want the skipped section", got)
	}
	if !strings.Contains(got, "12000 tokens (allocated)") {
		t.Errorf("got = %q, want the total tokens footer", got)
	}
}

func TestStepSummaryTableOmitsEmptySections(t *testing.T) {
	r := &Roster{Members: []Member{{ID: "security", Kind: "agent"}}}
	got := r.StepSummaryTable("## agentic-review")
	if strings.Contains(got, "verifier lenses:") {
		t.Errorf("got = %q, want no verifier line when Verifiers is empty", got)
	}
	if strings.Contains(got, "### Skipped") {
		t.Errorf("got = %q, want no skipped section when Skipped is empty", got)
	}
}
