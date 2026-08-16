package activation

import (
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/schema"
)

func TestCompileRuleFactsOnlyRejectsAssessment(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	_, err = CompileRule(checkEnv, `assessment.risk == RISK_LOW`, ClassFactsOnly, "skip_when")
	if err == nil {
		t.Fatalf("CompileRule accepted an assessment reference under ClassFactsOnly")
	}
}

// TestContextClass is Verification item 3's exact concrete expectation:
// `assessment.risk == RISK_LOW` under skip_when (facts-only) fails with an
// error naming the slot and "may reference facts only"; the identical
// expression under volunteer_on (facts+assessment) loads cleanly.
func TestContextClass(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}

	_, err = CompileRule(checkEnv, `assessment.risk == RISK_LOW`, ClassFactsOnly, "skip_when")
	if err == nil {
		t.Fatalf("CompileRule accepted an assessment reference under skip_when's facts-only class")
	}
	if !strings.Contains(err.Error(), "skip_when may reference facts only") {
		t.Errorf("err = %q, want it to contain %q", err.Error(), "skip_when may reference facts only")
	}

	if _, err := CompileRule(checkEnv, `assessment.risk == RISK_LOW`, ClassFactsAndAssessment, "volunteer_on"); err != nil {
		t.Errorf("CompileRule under volunteer_on's facts+assessment class: %v, want it to load cleanly", err)
	}
}

func TestCompileRuleFactsOnlyAcceptsFacts(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `"deps" in facts.diff.classes`, ClassFactsOnly, "skip_when")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if !rule.UsesFacts || rule.UsesAssessment {
		t.Errorf("rule = %+v, want UsesFacts=true UsesAssessment=false", rule)
	}
}

// A rule built entirely from custom functions (has_class, touches, ...)
// never literally references the facts identifier, even though those
// functions read facts internally — References is a literal identifier
// walk, not a data-flow analysis. It must still pass the facts-only lint,
// since none of the closed function library ever touches assessment.
func TestCompileRuleFactsOnlyAcceptsFunctionOnlyRule(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `has_class("deps")`, ClassFactsOnly, "skip_when")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if rule.UsesFacts || rule.UsesAssessment {
		t.Errorf("rule = %+v, want UsesFacts=false (no literal facts.* reference) and UsesAssessment=false", rule)
	}
}

func TestCompileRuleFactsAndAssessmentAcceptsBoth(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `facts.pr.is_fork || assessment.risk >= RISK_HIGH`, ClassFactsAndAssessment, "volunteer_on")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	if !rule.UsesFacts || !rule.UsesAssessment {
		t.Errorf("rule = %+v, want both true", rule)
	}
}

func TestCompileRuleSyntaxError(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	if _, err := CompileRule(checkEnv, `facts.pr.is_fork &&`, ClassFactsOnly, "skip_when"); err == nil {
		t.Fatalf("CompileRule accepted malformed CEL source")
	}
}

func TestEvaluateNormalBooleanRule(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `has_class("deps")`, ClassFactsOnly, "skip_when")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"deps"}}}
	matched, err := Evaluate(rule, f, nil)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !matched {
		t.Errorf("Evaluate = false, want true")
	}
}

func TestEvaluateAssessmentRuleWithNilAssessmentIsNotMatched(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `assessment.risk >= RISK_HIGH`, ClassFactsAndAssessment, "volunteer_on")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	f := &facts.Facts{}
	matched, err := Evaluate(rule, f, nil)
	if err != nil {
		t.Fatalf("Evaluate returned an error for an unbound assessment reference: %v", err)
	}
	if matched {
		t.Errorf("Evaluate = true, want false (triage failed, assessment unbound)")
	}
}

func TestEvaluateAssessmentRuleWithRealAssessment(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `assessment.risk >= RISK_HIGH`, ClassFactsAndAssessment, "volunteer_on")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	f := &facts.Facts{}
	a := &schema.Assessment{Risk: schema.RiskCritical}
	matched, err := Evaluate(rule, f, a)
	if err != nil {
		t.Fatalf("Evaluate: %v", err)
	}
	if !matched {
		t.Errorf("Evaluate = false, want true")
	}
}

func TestEvaluateNonBooleanResultErrors(t *testing.T) {
	checkEnv, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	rule, err := CompileRule(checkEnv, `facts.diff.additions`, ClassFactsOnly, "skip_when")
	if err != nil {
		t.Fatalf("CompileRule: %v", err)
	}
	f := &facts.Facts{Diff: facts.Diff{Additions: 5}}
	if _, err := Evaluate(rule, f, nil); err == nil {
		t.Fatalf("Evaluate accepted a non-boolean result")
	}
}
