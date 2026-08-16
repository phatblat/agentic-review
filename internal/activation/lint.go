package activation

import (
	"fmt"

	"github.com/google/cel-go/cel"
	celast "github.com/google/cel-go/common/ast"
)

// ContextClass is the §5.4 context-class lint's allowed-reference set for
// one activation slot.
type ContextClass int

const (
	// ClassFactsOnly permits facts.* only: skip_when, and required_when on
	// an immutable persona.
	ClassFactsOnly ContextClass = iota
	// ClassFactsAndAssessment permits facts.* and assessment.*:
	// volunteer_on, escalation `when`, and every other required_when.
	ClassFactsAndAssessment
)

// References walks the checked AST and reports whether it references the
// facts and/or assessment root identifiers.
func References(ast *cel.Ast) (usesFacts, usesAssessment bool) {
	nav := celast.NavigateAST(ast.NativeRep())
	idents := celast.MatchDescendants(nav, celast.KindMatcher(celast.IdentKind))
	for _, id := range idents {
		switch id.AsIdent() {
		case "facts":
			usesFacts = true
		case "assessment":
			usesAssessment = true
		}
	}
	return usesFacts, usesAssessment
}

// Lint enforces the §5.4 context-class table for one activation slot; slot
// is used only to identify the rule in the returned error. Violations are
// load errors — this is the security boundary that keeps model output
// (assessment.*) out of skip decisions and out of what an immutable
// persona's activation can be steered by.
func Lint(ast *cel.Ast, allowed ContextClass, slot string) error {
	_, usesAssessment := References(ast)
	if allowed == ClassFactsOnly && usesAssessment {
		return fmt.Errorf("activation: %s may reference facts only, not assessment: %s", slot, ast.Source().Content())
	}
	return nil
}
