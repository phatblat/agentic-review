package activation

import (
	"testing"

	"github.com/google/cel-go/cel"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/schema"
)

func evalRule(t *testing.T, env *cel.Env, source string, f *facts.Facts, a *schema.Assessment) (any, error) {
	t.Helper()
	ast, iss := env.Compile(source)
	if iss.Err() != nil {
		return nil, iss.Err()
	}
	prg, err := env.Program(ast, cel.CostLimit(100_000), cel.EvalOptions(cel.OptTrackCost))
	if err != nil {
		return nil, err
	}
	var factsVal any = f
	var assessmentVal any = a
	if f == nil {
		factsVal = &facts.Facts{}
	}
	if a == nil {
		assessmentVal = &schema.Assessment{}
	}
	out, _, err := prg.Eval(map[string]any{"facts": factsVal, "assessment": assessmentVal})
	if err != nil {
		return nil, err
	}
	return out.Value(), nil
}

func TestCheckEnvFunctionsAreUnavailable(t *testing.T) {
	env, err := NewEnv(nil, nil)
	if err != nil {
		t.Fatalf("NewEnv(nil, nil): %v", err)
	}
	if _, err := evalRule(t, env, `touches("**/*.go")`, nil, nil); err == nil {
		t.Fatalf("touches() succeeded against the check env, want an error")
	}
}

func TestEvalEnvTouches(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Paths: []string{"internal/auth/token.go", "README.md"}}}
	env, err := NewEnv(f, nil)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	got, err := evalRule(t, env, `touches("**/auth/**")`, f, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != true {
		t.Errorf("touches(\"**/auth/**\") = %v, want true", got)
	}
	got, err = evalRule(t, env, `touches("**/*.rs")`, f, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != false {
		t.Errorf("touches(\"**/*.rs\") = %v, want false", got)
	}
}

func TestEvalEnvTouchesOnly(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Paths: []string{"docs/a.md", "docs/b.md"}}}
	env, err := NewEnv(f, nil)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	got, err := evalRule(t, env, `touches_only(["docs/**"])`, f, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != true {
		t.Errorf("touches_only([\"docs/**\"]) = %v, want true", got)
	}

	f2 := &facts.Facts{Diff: facts.Diff{Paths: []string{"docs/a.md", "src/main.go"}}}
	env2, err := NewEnv(f2, nil)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	got, err = evalRule(t, env2, `touches_only(["docs/**"])`, f2, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != false {
		t.Errorf("touches_only with a non-matching path = %v, want false", got)
	}
}

func TestEvalEnvHasClassAndSizeShorthands(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{
		Classes:      []string{"deps", "source"},
		Additions:    50,
		FilesChanged: 3,
	}}
	env, err := NewEnv(f, nil)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	cases := map[string]bool{
		`has_class("deps")`: true,
		`has_class("docs")`: false,
		`added_over(10)`:    true,
		`added_over(1000)`:  false,
		`files_over(1)`:     true,
		`files_over(100)`:   false,
	}
	for src, want := range cases {
		got, err := evalRule(t, env, src, f, nil)
		if err != nil {
			t.Fatalf("Eval(%q): %v", src, err)
		}
		if got != want {
			t.Errorf("%s = %v, want %v", src, got, want)
		}
	}
}

func TestEvalEnvDepBumped(t *testing.T) {
	f := &facts.Facts{Deps: facts.Deps{Changed: []facts.DepChange{
		{Ecosystem: "cargo", Name: "openssl", From: "1.0.2", To: "3.2.1", Bump: "major"},
	}}}
	env, err := NewEnv(f, nil)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	got, err := evalRule(t, env, `dep_bumped("cargo", "minor")`, f, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != true {
		t.Errorf(`dep_bumped("cargo", "minor") = %v, want true (major >= minor)`, got)
	}
	got, err = evalRule(t, env, `dep_bumped("npm", "patch")`, f, nil)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != false {
		t.Errorf(`dep_bumped("npm", "patch") = %v, want false (no npm changes)`, got)
	}
}

func TestEvalEnvFieldAccessAndConstants(t *testing.T) {
	f := &facts.Facts{PR: facts.PR{IsFork: true, AuthorAssociation: facts.AssocFirstTimeContributor}}
	a := &schema.Assessment{Risk: schema.RiskHigh}
	env, err := NewEnv(f, a)
	if err != nil {
		t.Fatalf("NewEnv: %v", err)
	}
	got, err := evalRule(t, env, `facts.pr.is_fork && facts.pr.author_association > ASSOC_COLLABORATOR`, f, a)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != true {
		t.Errorf("fork-guard rule = %v, want true", got)
	}
	got, err = evalRule(t, env, `assessment.risk >= RISK_HIGH`, f, a)
	if err != nil {
		t.Fatalf("Eval: %v", err)
	}
	if got != true {
		t.Errorf("assessment.risk >= RISK_HIGH = %v, want true", got)
	}
}
