package activation

import (
	"fmt"
	"strconv"
	"strings"
)

// Compile turns one Trigger group into a single CEL source string, AND-ing
// together whichever keys are present, in this fixed order: paths,
// languages, classes, labels, domains, expr. The generated source is
// stored on the compiled rule and emitted verbatim in roster.json and the
// step summary — spec §5 requires the compiled expression to be visible.
func Compile(t Trigger) (string, error) {
	var clauses []string

	if len(t.Paths) > 0 {
		clauses = append(clauses, orClause(t.Paths, func(v string) string {
			return fmt.Sprintf("touches(%s)", quote(v))
		}))
	}
	if len(t.Languages) > 0 {
		clauses = append(clauses, orClause(t.Languages, func(v string) string {
			return fmt.Sprintf("%s in facts.diff.languages", quote(v))
		}))
	}
	if len(t.Classes) > 0 {
		clauses = append(clauses, orClause(t.Classes, func(v string) string {
			return fmt.Sprintf("has_class(%s)", quote(v))
		}))
	}
	if len(t.Labels) > 0 {
		clauses = append(clauses, orClause(t.Labels, func(v string) string {
			return fmt.Sprintf("%s in facts.pr.labels", quote(v))
		}))
	}
	if len(t.Domains) > 0 {
		clauses = append(clauses, fmt.Sprintf("assessment.domains.exists(d, d in %s)", quoteList(t.Domains)))
	}
	if t.Expr != "" {
		clauses = append(clauses, "("+t.Expr+")")
	}

	if len(clauses) == 0 {
		return "", fmt.Errorf("activation: trigger group has no conditions set")
	}
	return strings.Join(clauses, " && "), nil
}

// CompileAny sugar-compiles every trigger group and ORs the results into
// one expression — the volunteer_on semantics ("any of these groups
// matches"). An empty slice compiles to "false".
func CompileAny(triggers []Trigger) (string, error) {
	if len(triggers) == 0 {
		return "false", nil
	}
	parts := make([]string, len(triggers))
	for i, t := range triggers {
		src, err := Compile(t)
		if err != nil {
			return "", fmt.Errorf("activation: volunteer_on group %d: %w", i, err)
		}
		parts[i] = "(" + src + ")"
	}
	return strings.Join(parts, " || "), nil
}

func orClause(values []string, render func(string) string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = render(v)
	}
	if len(parts) == 1 {
		return parts[0]
	}
	return "(" + strings.Join(parts, " || ") + ")"
}

func quote(s string) string {
	return strconv.Quote(s)
}

func quoteList(values []string) string {
	parts := make([]string, len(values))
	for i, v := range values {
		parts[i] = quote(v)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}
