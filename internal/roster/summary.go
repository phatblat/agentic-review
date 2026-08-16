package roster

import (
	"fmt"
	"strings"
	"text/tabwriter"
)

// StepSummaryTable renders r as the $GITHUB_STEP_SUMMARY table spec §13.4
// requires every run to write: persona, kind, model, activation reason,
// budget, and skip reasons. heading lets each caller (plan, review) title
// the table for its own context.
func (r *Roster) StepSummaryTable(heading string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s\n\n", heading)

	tw := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "persona\tkind\tmodel\tactivation reason\tmax tokens")
	for _, m := range r.Members {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d\n", m.ID, m.Kind, m.ResolvedModel, m.ActivationReason, m.MaxTokens)
	}
	_ = tw.Flush()
	b.WriteString("\n")

	if len(r.Verifiers) > 0 {
		fmt.Fprintf(&b, "verifier lenses: %s\n\n", strings.Join(r.Verifiers, ", "))
	}

	if len(r.Skipped) > 0 {
		b.WriteString("### Skipped\n\n")
		tw2 := tabwriter.NewWriter(&b, 0, 2, 2, ' ', 0)
		_, _ = fmt.Fprintln(tw2, "persona\treason")
		for _, s := range r.Skipped {
			_, _ = fmt.Fprintf(tw2, "%s\t%s\n", s.ID, s.Reason)
		}
		_ = tw2.Flush()
	}

	fmt.Fprintf(&b, "\n🔢 %d tokens (allocated)\n", r.TotalTokens)
	return b.String()
}
