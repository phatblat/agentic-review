package activation

import (
	"reflect"

	"github.com/google/cel-go/cel"
	"github.com/google/cel-go/common/types"
	"github.com/google/cel-go/ext"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/schema"
)

// errUnavailable is returned by every custom function's check-env binding:
// there is no run to evaluate against yet, only type-checking.
const errUnavailable = "function unavailable during validation"

// NewEnv builds the CEL environment for spec §5's activation grammar.
//
// NewEnv(nil, nil) is the check env, used at config load time for
// validation and the context-class lint: every custom function binding
// returns an error, since there is no fact set to evaluate against. A
// per-run eval env is built by passing the real f (and a, once triage has
// run); its function bindings close over f. Rules are compiled with the
// check env at config load and recompiled with the eval env at run time —
// rule counts are small, so double compilation is free.
//
// No env.Option beyond what is listed here is ever added: no ext.Strings,
// no macros beyond the CEL standard library, no cel.OptionalTypes, no
// now(), no I/O.
func NewEnv(f *facts.Facts, a *schema.Assessment) (*cel.Env, error) {
	opts := []cel.EnvOption{
		ext.NativeTypes(ext.ParseStructTags(true),
			reflect.TypeOf(facts.Facts{}),
			reflect.TypeOf(facts.PR{}),
			reflect.TypeOf(facts.Diff{}),
			reflect.TypeOf(facts.DepChange{}),
			reflect.TypeOf(facts.Deps{}),
			reflect.TypeOf(schema.Assessment{}),
		),
		cel.Variable("facts", cel.ObjectType("facts.Facts")),
		cel.Variable("assessment", cel.ObjectType("schema.Assessment")),

		cel.Constant("RISK_LOW", cel.IntType, types.Int(schema.RiskLow)),
		cel.Constant("RISK_MODERATE", cel.IntType, types.Int(schema.RiskModerate)),
		cel.Constant("RISK_HIGH", cel.IntType, types.Int(schema.RiskHigh)),
		cel.Constant("RISK_CRITICAL", cel.IntType, types.Int(schema.RiskCritical)),

		cel.Constant("COMPLEXITY_TRIVIAL", cel.IntType, types.Int(schema.ComplexityTrivial)),
		cel.Constant("COMPLEXITY_SIMPLE", cel.IntType, types.Int(schema.ComplexitySimple)),
		cel.Constant("COMPLEXITY_MODERATE", cel.IntType, types.Int(schema.ComplexityModerate)),
		cel.Constant("COMPLEXITY_COMPLEX", cel.IntType, types.Int(schema.ComplexityComplex)),

		cel.Constant("ASSOC_OWNER", cel.IntType, types.Int(facts.AssocOwner)),
		cel.Constant("ASSOC_MEMBER", cel.IntType, types.Int(facts.AssocMember)),
		cel.Constant("ASSOC_COLLABORATOR", cel.IntType, types.Int(facts.AssocCollaborator)),
		cel.Constant("ASSOC_CONTRIBUTOR", cel.IntType, types.Int(facts.AssocContributor)),
		cel.Constant("ASSOC_FIRST_TIME_CONTRIBUTOR", cel.IntType, types.Int(facts.AssocFirstTimeContributor)),
		cel.Constant("ASSOC_NONE", cel.IntType, types.Int(facts.AssocNone)),
	}
	opts = append(opts, functionOptions(f)...)

	return cel.NewEnv(opts...)
}
