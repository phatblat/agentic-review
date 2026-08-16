package activation

import (
	"fmt"

	"github.com/google/cel-go/cel"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/schema"
)

// CompiledRule is one activation rule after sugar compilation and check-env
// validation: its generated source (emitted verbatim in roster.json and
// the step summary) plus the check-env AST used for References/Lint.
// Evaluate recompiles Source against a fresh eval env at run time — rule
// counts are small, so double compilation is free.
type CompiledRule struct {
	Source         string
	CheckAst       *cel.Ast
	UsesFacts      bool
	UsesAssessment bool
}

// CompileRule sugar-compiles source against the check env (NewEnv(nil,
// nil)), so a load-time error reports a type/reference problem before any
// run starts. slot and allowed drive the §5.4 context-class lint.
func CompileRule(checkEnv *cel.Env, source string, allowed ContextClass, slot string) (*CompiledRule, error) {
	ast, iss := checkEnv.Compile(source)
	if iss.Err() != nil {
		return nil, fmt.Errorf("activation: %s: %w", slot, iss.Err())
	}
	if err := Lint(ast, allowed, slot); err != nil {
		return nil, err
	}
	usesFacts, usesAssessment := References(ast)
	return &CompiledRule{Source: source, CheckAst: ast, UsesFacts: usesFacts, UsesAssessment: usesAssessment}, nil
}

// Evaluate runs rule against f (and a, once triage has produced an
// assessment). When triage failed (a == nil), a rule that references
// assessment evaluates to false and logs a debug line — a rule referencing
// an unbound namespace is "not matched", never an error (spec §5.1). A
// non-boolean result is a run error.
func Evaluate(rule *CompiledRule, f *facts.Facts, a *schema.Assessment) (bool, error) {
	if a == nil && rule.UsesAssessment {
		logx.Debug("activation: %q references assessment but triage failed; treating as not matched", rule.Source)
		return false, nil
	}

	env, err := NewEnv(f, a)
	if err != nil {
		return false, fmt.Errorf("activation: build eval env: %w", err)
	}
	ast, iss := env.Compile(rule.Source)
	if iss.Err() != nil {
		return false, fmt.Errorf("activation: recompile %q against eval env: %w", rule.Source, iss.Err())
	}
	prg, err := env.Program(ast, cel.CostLimit(100_000), cel.EvalOptions(cel.OptTrackCost))
	if err != nil {
		return false, fmt.Errorf("activation: build program for %q: %w", rule.Source, err)
	}

	out, _, err := prg.Eval(map[string]any{"facts": f, "assessment": assessmentOrEmpty(a)})
	if err != nil {
		return false, fmt.Errorf("activation: evaluate %q: %w", rule.Source, err)
	}
	b, ok := out.Value().(bool)
	if !ok {
		return false, fmt.Errorf("activation: %q evaluated to non-boolean %T", rule.Source, out.Value())
	}
	return b, nil
}

// assessmentOrEmpty substitutes a zero-value Assessment when a is nil, so a
// rule that (per the UsesAssessment short-circuit above) is known not to
// reference assessment can still be evaluated without a nil pointer.
func assessmentOrEmpty(a *schema.Assessment) *schema.Assessment {
	if a != nil {
		return a
	}
	return &schema.Assessment{}
}
