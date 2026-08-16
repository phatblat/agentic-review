package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gate"
	"github.com/phatblat/agentic-review/internal/localconfig"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
)

func init() {
	commands["plan"] = cmdPlan
}

// cmdPlan implements `agentic-review plan --triage triage.json --config .`:
// runs the tier-0 skip decision plus roster computation with zero model
// calls, printing roster.json and the step-summary table.
func cmdPlan(_ context.Context, args []string) int {
	fs := flag.NewFlagSet("plan", flag.ContinueOnError)
	triagePath := fs.String("triage", "", "path to a triage.json record (required)")
	configDir := fs.String("config", ".", "repo root containing .github/agentic-review/")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *triagePath == "" {
		fmt.Fprintln(os.Stderr, "plan: --triage is required")
		return 1
	}

	data, err := os.ReadFile(*triagePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: read %s: %v\n", *triagePath, err)
		return 1
	}
	var record struct {
		Facts      facts.Facts        `json:"facts"`
		Assessment *schema.Assessment `json:"assessment"`
	}
	if err := json.Unmarshal(data, &record); err != nil {
		fmt.Fprintf(os.Stderr, "plan: decode %s: %v\n", *triagePath, err)
		return 1
	}

	reg, _, cfg, err := localconfig.Load(*configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		return 1
	}

	skip, reason, err := gate.Skip(&record.Facts, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		return 1
	}
	if skip {
		_, _ = fmt.Fprintf(os.Stdout, "skipped agentic review: %s\n", reason)
		_ = logx.StepSummary(fmt.Sprintf("## agentic-review plan\n\n✅ skipped agentic review: %s\n", reason))
		return 0
	}

	r, err := roster.Compute(reg, &record.Facts, record.Assessment, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "plan: %v\n", err)
		return 1
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(r); err != nil {
		fmt.Fprintf(os.Stderr, "plan: encode roster: %v\n", err)
		return 1
	}

	_ = logx.StepSummary(r.StepSummaryTable("## agentic-review plan"))
	return 0
}
