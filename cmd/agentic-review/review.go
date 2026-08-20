package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/osv"
	"github.com/phatblat/agentic-review/internal/runner"
)

func init() {
	commands["review"] = cmdReview
}

// cmdReview implements `agentic-review review --event $GITHUB_EVENT_PATH
// [--record recordings/]`: the live entrypoint driven by a GitHub Actions
// workflow, wiring internal/runner.Review to the real GitHub API and
// inference endpoint.
//
// AGENTIC_REVIEW_DRY_RUN=1 (spec item 9's shim smoke test) swaps the real
// GitHub client for a gh.Fake loaded from the event file's own directory
// — the fixture doubles as both the webhook payload and the fake API
// data — and points ConfigRoot at that same directory unless --config
// was set explicitly. No $GITHUB_TOKEN is required in this mode; the
// pipeline never opens a network socket.
func cmdReview(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("review", flag.ContinueOnError)
	eventPath := fs.String("event", os.Getenv("GITHUB_EVENT_PATH"), "path to the webhook event payload JSON")
	recordDir := fs.String("record", "", "directory to record model request/response pairs into")
	outDir := fs.String("out", "", "artifact output directory (default: $RUNNER_TEMP/agentic-review or ./.agentic-review/run)")
	configRoot := fs.String("config", ".", "repo root containing .github/agentic-review/ (or that directory itself)")
	apiKeyEnv := fs.String("api-key-env", "AGENTIC_REVIEW_API_KEY", "env var holding the inference API key")
	endpoint := fs.String("endpoint", os.Getenv("AGENTIC_REVIEW_ENDPOINT"), "default endpoint for any models[] binding config.yaml leaves blank")
	failOn := fs.String("fail-on", os.Getenv("AGENTIC_REVIEW_FAIL_ON"), "override cfg.review.gate.fail_on when set")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if *eventPath == "" {
		fmt.Fprintln(os.Stderr, "review: --event or $GITHUB_EVENT_PATH is required")
		return 1
	}
	eventName := os.Getenv("GITHUB_EVENT_NAME")
	if eventName == "" {
		fmt.Fprintln(os.Stderr, "review: $GITHUB_EVENT_NAME is required")
		return 1
	}

	payload, err := os.ReadFile(*eventPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "review: read %s: %v\n", *eventPath, err)
		return 1
	}

	dryRun := os.Getenv("AGENTIC_REVIEW_DRY_RUN") == "1"

	var port gh.Port
	if dryRun {
		fixtureDir := filepath.Dir(*eventPath)
		fake, err := gh.LoadFake(fixtureDir)
		if err != nil {
			fmt.Fprintf(os.Stderr, "review: dry-run: load fake GitHub data from %s: %v\n", fixtureDir, err)
			return 1
		}
		port = fake
		if *configRoot == "." {
			*configRoot = fixtureDir
		}
	} else {
		token := os.Getenv("GITHUB_TOKEN")
		if token == "" {
			fmt.Fprintln(os.Stderr, "review: $GITHUB_TOKEN is required")
			return 1
		}
		port = gh.NewGitHub(token)
	}

	runID, _ := strconv.ParseInt(os.Getenv("GITHUB_RUN_ID"), 10, 64)
	runURL := ""
	if repo := os.Getenv("GITHUB_REPOSITORY"); repo != "" && runID != 0 {
		runURL = fmt.Sprintf("https://github.com/%s/actions/runs/%d", repo, runID)
	}

	var cl infer.Client = infer.NewHTTPClient(os.Getenv(*apiKeyEnv), inferenceHeaders())

	exit := runner.Review(ctx, eventName, payload, runner.ReviewDeps{
		Port:             port,
		Client:           cl,
		OSVClient:        osv.NewClient(),
		DepsDevClient:    osv.NewDepsDevClient(),
		ConfigRoot:       *configRoot,
		RunID:            runID,
		RunURL:           runURL,
		OutDir:           *outDir,
		RecordDir:        *recordDir,
		DryRun:           dryRun,
		EndpointOverride: *endpoint,
		FailOnOverride:   *failOn,
	})
	return exit
}
