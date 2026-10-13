// Command agentic-review performs variable-sized code review on GitHub pull
// requests using locally hosted models on self-hosted runners. See
// docs/spec.md for the full design.
package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
)

// version is the app version. `just version` keeps it equal to
// shim/index.mjs's EXPECTED_VERSION (TestVersionMatchesShim fails on
// drift), so a plain `go build` passes the shim's version check. Release
// builds stamp the tag over it via .goreleaser.yaml's
// -ldflags "-X main.version={{ .Tag }}".
var version = "v0.1.0"

const usage = `agentic-review review   --event $GITHUB_EVENT_PATH [--record recordings/]
agentic-review plan     --triage triage.json --config .        # no model calls
agentic-review triage   --diff pr.diff [--record recordings/]  # live
agentic-review validate                                        # parse/type-check all config
agentic-review fetch <pr|run|job url> [--out fixtures/] [--run <id>]
`

// command is one subcommand's entrypoint: it owns its own flag.FlagSet and
// returns the process exit code.
type command func(ctx context.Context, args []string) int

// commands is the subcommand dispatch map. Each subcommand registers itself
// from its own file via init() as its milestone lands, so this map never
// references a package that does not yet exist.
var commands = map[string]command{}

func main() {
	os.Exit(run(context.Background(), os.Args[1:]))
}

func run(ctx context.Context, args []string) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprint(os.Stderr, usage)
		return 1
	}
	if args[0] == "version" {
		_, _ = fmt.Fprintln(os.Stdout, version)
		return 0
	}
	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(os.Stderr, "agentic-review: unknown subcommand %q\n\n%s", args[0], usage)
		return 1
	}
	return cmd(ctx, args[1:])
}

// inferenceHeaders builds the headers every live inference request
// carries beyond Content-Type/Authorization: x-session-id set to
// $GITHUB_RUN_ID when the process has one, for Casper/SR run
// attribution telemetry (spec §10). Both `review` (the GitHub Action
// entrypoint) and standalone `triage` call this, so the Action and a
// direct CLI invocation share one attribution rule; shim/index.mjs
// already passes the inherited process environment through to this
// binary, so no shim change is needed. An empty/absent GITHUB_RUN_ID
// (local CLI usage) omits the header rather than inventing an unstable
// identifier.
func inferenceHeaders() http.Header {
	h := http.Header{}
	if runID := os.Getenv("GITHUB_RUN_ID"); runID != "" {
		h.Set("x-session-id", runID)
	}
	return h
}
