package gate

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
)

func TestSkipDepsOnly(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"deps"}}}
	cfg := config.Defaults()
	skip, reason, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if !skip || reason != "deps-only change" {
		t.Errorf("skip=%v reason=%q, want true \"deps-only change\"", skip, reason)
	}
}

func TestSkipDepsAndDocs(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"docs", "deps"}}}
	cfg := config.Defaults()
	skip, reason, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if !skip || reason != "deps+docs-only change" {
		t.Errorf("skip=%v reason=%q, want true \"deps+docs-only change\"", skip, reason)
	}
}

func TestSkipSourceChangeDoesNotSkip(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"deps", "source"}}}
	cfg := config.Defaults()
	skip, _, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if skip {
		t.Errorf("skip = true, want false: source is not in the default skip_classes")
	}
}

func TestSkipEmptyClassesDoesNotSkip(t *testing.T) {
	f := &facts.Facts{}
	cfg := config.Defaults()
	skip, _, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if skip {
		t.Errorf("skip = true, want false: no classes present means nothing was even classified")
	}
}

func TestSkipWhenRule(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"source"}, FilesChanged: 1}}
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{`facts.diff.files_changed == 1 && has_class("source")`}
	skip, reason, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if !skip || reason != cfg.Review.SkipWhen[0] {
		t.Errorf("skip=%v reason=%q, want true %q", skip, reason, cfg.Review.SkipWhen[0])
	}
}

func TestSkipWhenRuleNotMatched(t *testing.T) {
	f := &facts.Facts{Diff: facts.Diff{Classes: []string{"source"}, FilesChanged: 5}}
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{`facts.diff.files_changed == 1`}
	skip, _, err := Skip(f, cfg)
	if err != nil {
		t.Fatalf("Skip: %v", err)
	}
	if skip {
		t.Errorf("skip = true, want false")
	}
}

func TestSkipWhenAssessmentReferenceIsLoadError(t *testing.T) {
	f := &facts.Facts{}
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{`assessment.risk == RISK_LOW`}
	if _, _, err := Skip(f, cfg); err == nil {
		t.Fatalf("Skip accepted a skip_when rule referencing assessment")
	}
}

func TestSkipWhenMalformedExpressionErrors(t *testing.T) {
	f := &facts.Facts{}
	cfg := config.Defaults()
	cfg.Review.SkipWhen = []string{`facts.diff.additions >`}
	if _, _, err := Skip(f, cfg); err == nil {
		t.Fatalf("Skip accepted a malformed skip_when expression")
	}
}
