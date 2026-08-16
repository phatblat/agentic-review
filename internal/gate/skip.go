// Package gate implements the tier-0 skip decision (Skip) and, once tier-2
// findings have been rendered, the exit-code decision (Exit).
package gate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
)

// Skip implements the tier-0 skip decision: skip when the diff has at
// least one class and every class is in cfg.Review.SkipClasses, or when
// any cfg.Review.SkipWhen CEL expression evaluates true. reason is the
// summary text: "deps-only change", "docs-only change",
// "deps+docs-only change", or the source of the matching skip_when rule.
//
// Unlike the spec's two-value sketch, Skip also returns an error: a
// malformed skip_when expression must fail the run before models start,
// not be silently treated as "not matched".
func Skip(f *facts.Facts, cfg *config.Config) (skip bool, reason string, err error) {
	if len(f.Diff.Classes) > 0 && allSkippable(f.Diff.Classes, cfg.Review.SkipClasses) {
		return true, classesReason(f.Diff.Classes), nil
	}

	if len(cfg.Review.SkipWhen) == 0 {
		return false, "", nil
	}

	checkEnv, err := activation.NewEnv(nil, nil)
	if err != nil {
		return false, "", fmt.Errorf("gate: build check env: %w", err)
	}
	for _, source := range cfg.Review.SkipWhen {
		rule, err := activation.CompileRule(checkEnv, source, activation.ClassFactsOnly, "skip_when")
		if err != nil {
			return false, "", fmt.Errorf("gate: %w", err)
		}
		matched, err := activation.Evaluate(rule, f, nil)
		if err != nil {
			return false, "", fmt.Errorf("gate: evaluate skip_when %q: %w", source, err)
		}
		if matched {
			return true, source, nil
		}
	}
	return false, "", nil
}

func allSkippable(classes, skipClasses []string) bool {
	set := make(map[string]bool, len(skipClasses))
	for _, c := range skipClasses {
		set[c] = true
	}
	for _, c := range classes {
		if !set[c] {
			return false
		}
	}
	return true
}

// classesReason renders "deps-only change" / "docs-only change" /
// "deps+docs-only change" from the sorted, deduplicated class list.
func classesReason(classes []string) string {
	uniq := make(map[string]bool, len(classes))
	for _, c := range classes {
		uniq[c] = true
	}
	names := make([]string, 0, len(uniq))
	for c := range uniq {
		names = append(names, c)
	}
	sort.Strings(names)
	return strings.Join(names, "+") + "-only change"
}
