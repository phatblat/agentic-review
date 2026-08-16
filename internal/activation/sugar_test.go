package activation

import "testing"

func TestCompilePaths(t *testing.T) {
	src, err := Compile(Trigger{Paths: []string{"**/auth/**"}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if src != `touches("**/auth/**")` {
		t.Errorf("src = %q", src)
	}
}

func TestCompileMultiplePathsOred(t *testing.T) {
	src, err := Compile(Trigger{Paths: []string{"a/**", "b/**"}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `(touches("a/**") || touches("b/**"))`
	if src != want {
		t.Errorf("src = %q, want %q", src, want)
	}
}

func TestCompileMultipleKeysAnded(t *testing.T) {
	src, err := Compile(Trigger{Paths: []string{"**/auth/**"}, Labels: []string{"security"}})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `touches("**/auth/**") && "security" in facts.pr.labels`
	if src != want {
		t.Errorf("src = %q, want %q", src, want)
	}
}

func TestCompileFixedKeyOrder(t *testing.T) {
	// languages, classes, labels, domains, expr all present: must render in
	// paths -> languages -> classes -> labels -> domains -> expr order
	// regardless of struct literal order.
	src, err := Compile(Trigger{
		Expr:      "facts.pr.commits > 1",
		Domains:   []string{"auth"},
		Labels:    []string{"deps"},
		Classes:   []string{"deps"},
		Languages: []string{"rust"},
	})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	want := `"rust" in facts.diff.languages && has_class("deps") && "deps" in facts.pr.labels && assessment.domains.exists(d, d in ["auth"]) && (facts.pr.commits > 1)`
	if src != want {
		t.Errorf("src = %q, want %q", src, want)
	}
}

func TestCompileExprOnly(t *testing.T) {
	src, err := Compile(Trigger{Expr: "facts.pr.is_fork"})
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if src != "(facts.pr.is_fork)" {
		t.Errorf("src = %q", src)
	}
}

func TestCompileEmptyTriggerErrors(t *testing.T) {
	if _, err := Compile(Trigger{}); err == nil {
		t.Fatalf("Compile(Trigger{}) succeeded, want an error")
	}
}

func TestCompileAny(t *testing.T) {
	src, err := CompileAny([]Trigger{
		{Paths: []string{"**/auth/**"}},
		{Domains: []string{"secrets"}},
	})
	if err != nil {
		t.Fatalf("CompileAny: %v", err)
	}
	want := `(touches("**/auth/**")) || (assessment.domains.exists(d, d in ["secrets"]))`
	if src != want {
		t.Errorf("src = %q, want %q", src, want)
	}
}

func TestCompileAnyEmpty(t *testing.T) {
	src, err := CompileAny(nil)
	if err != nil {
		t.Fatalf("CompileAny(nil): %v", err)
	}
	if src != "false" {
		t.Errorf("src = %q, want false", src)
	}
}
