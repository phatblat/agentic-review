package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/localconfig"
	"github.com/phatblat/agentic-review/internal/validate"
)

func init() {
	commands["validate"] = cmdValidate
}

// cmdValidate implements `agentic-review validate [dir]`: loads builtin +
// repo-local personas and config.yaml from the working directory (not
// the API) via localconfig.Load — the same M3 resolution path
// internal/runner.Review uses — then exhaustively compile + context-class
// lint checks every M4 rule via internal/validate.All. Prints one line
// per rule and exits 1 on any failure, 0 otherwise (spec item 41b).
func cmdValidate(_ context.Context, args []string) int {
	root := "."
	if len(args) > 0 {
		root = args[0]
	}

	reg, _, cfg, err := localconfig.Load(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL config/persona resolution: %v\n", err)
		return 1
	}

	checks, err := validate.All(reg, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "FAIL build check env: %v\n", err)
		return 1
	}

	exit := 0
	for _, c := range checks {
		if c.Err != nil {
			exit = 1
			fmt.Printf("FAIL %-28s %-16s %v\n", checkLabel(c), contextClassLabel(c.Allowed), c.Err)
			continue
		}
		fmt.Printf("OK   %-28s %-16s %s\n", checkLabel(c), contextClassLabel(c.Allowed), c.Source)
	}
	return exit
}

// checkLabel renders "<persona> <slot>" for a persona-level rule (e.g.
// "security volunteer_on[0]"), or the bare slot for a config-level rule
// (e.g. "escalation[0].when").
func checkLabel(c validate.RuleCheck) string {
	if c.PersonaID == "" {
		return c.Slot
	}
	return c.PersonaID + " " + strings.TrimPrefix(c.Slot, c.PersonaID+".")
}

func contextClassLabel(a activation.ContextClass) string {
	if a == activation.ClassFactsOnly {
		return "facts-only"
	}
	return "facts+assessment"
}
