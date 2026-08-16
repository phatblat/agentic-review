package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/diffscan"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/runner"
	"github.com/phatblat/agentic-review/internal/schema"
)

func init() {
	commands["triage"] = cmdTriage
}

// cmdTriage implements `agentic-review triage --diff pr.diff [--record dir]`:
// runs the triage stage standalone against a diff file (no GitHub API, no
// PR metadata beyond what the diff itself carries) and prints triage.json.
func cmdTriage(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("triage", flag.ContinueOnError)
	diffPath := fs.String("diff", "", "path to a unified diff file (required)")
	recordDir := fs.String("record", "", "directory to record model request/response pairs into")
	endpoint := fs.String("endpoint", os.Getenv("AGENTIC_REVIEW_ENDPOINT"), "inference endpoint for the triage capability")
	model := fs.String("model", "", "model name for the triage capability")
	apiKeyEnv := fs.String("api-key-env", "AGENTIC_REVIEW_API_KEY", "env var holding the inference API key")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *diffPath == "" {
		fmt.Fprintln(os.Stderr, "triage: --diff is required")
		return 1
	}
	if *endpoint == "" {
		fmt.Fprintln(os.Stderr, "triage: --endpoint or $AGENTIC_REVIEW_ENDPOINT is required")
		return 1
	}

	diffBytes, err := os.ReadFile(*diffPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "triage: read %s: %v\n", *diffPath, err)
		return 1
	}

	f := factsFromDiff(string(diffBytes))

	builtins, prompts, err := persona.Builtin()
	if err != nil {
		fmt.Fprintf(os.Stderr, "triage: %v\n", err)
		return 1
	}
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{
		"triage": {Endpoint: *endpoint, Model: *model, APIKeyEnv: *apiKeyEnv},
	}
	reg, err := persona.Resolve(builtins, nil, cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "triage: %v\n", err)
		return 1
	}

	var cl infer.Client = infer.NewHTTPClient(os.Getenv(*apiKeyEnv))
	if *recordDir != "" {
		cl = infer.NewRecorder(*recordDir, "triage", cl)
	}

	assessment, err := runner.RunTriage(ctx, cl, reg, prompts, cfg, f, runner.TriageInput{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "triage: %v\n", err)
		return 1
	}

	out := struct {
		Schema     string             `json:"schema"`
		Facts      *facts.Facts       `json:"facts"`
		Assessment *schema.Assessment `json:"assessment"`
	}{Schema: "triage/v1", Facts: f, Assessment: assessment}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		fmt.Fprintf(os.Stderr, "triage: encode output: %v\n", err)
		return 1
	}
	return 0
}

// factsFromDiff builds a best-effort Facts from a bare diff file, with no
// GitHub API access: PR-level fields (number, base_ref, author
// association, labels, draft, commits) are zero-valued, since a diff file
// carries none of them. Dependency-change detection is skipped — it needs
// separate base/head file content, which a diff alone does not provide.
func factsFromDiff(diff string) *facts.Facts {
	fileDiffs := diffscan.SplitFiles(diff)

	langAdditions := map[string]int{}
	pathsSet := map[string]bool{}
	classSet := map[string]bool{}
	var additions, deletions, maxFileAdditions int

	for _, fd := range fileDiffs {
		a, d := diffscan.CountChanges(fd.Patch)
		additions += a
		deletions += d
		if a > maxFileAdditions {
			maxFileAdditions = a
		}
		pathsSet[fd.Path] = true
		langAdditions[facts.LanguageOf(fd.Path)] += a

		file := gh.File{Path: fd.Path, Patch: fd.Patch, Additions: a, Deletions: d}
		cls, _ := classes.Classify(classes.Input{File: file})
		classSet[string(cls)] = true
	}

	return &facts.Facts{
		Diff: facts.Diff{
			FilesChanged:     len(fileDiffs),
			Additions:        additions,
			Deletions:        deletions,
			Languages:        langAdditions,
			Paths:            sortedKeys(pathsSet),
			Classes:          sortedKeys(classSet),
			MaxFileAdditions: maxFileAdditions,
		},
	}
}

func sortedKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
