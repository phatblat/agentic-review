# Repository Guidelines

## Project Overview

`agentic-review` is a Go CLI, packaged as a GitHub Action, that performs variable-sized
agentic code review on GitHub pull requests using locally hosted (OpenAI-compatible)
models on self-hosted runners. Review effort scales with change risk: trivial changes
(deps-only bumps, docs) exit through deterministic classification with **zero model
calls**; risky/complex changes assemble a configurable team of reviewer personas whose
findings pass through a fixed verification pipeline before being posted as PR comments.

The authoritative design doc is [`docs/spec.md`](docs/spec.md) — read it before making
behavioral changes; this file (and `README.md`) cover operational/contributor context
only. The deployment runbook is [`docs/setup.md`](docs/setup.md).

## Architecture & Data Flow

Three-tier, deterministic-shell/agentic-core pipeline. Everything outside actual model
judgment calls (routing, roster assembly, gating, posting) is pure Go — models only run
under structured-output (`json_schema`) constraints and their output is always treated as
**untrusted input**.

```
GitHub webhook (pull_request / issue_comment)
        │
        ▼
internal/ghevent  ── Parse/Gate: extract event, dedup re-runs on same commit
        │
        ▼
internal/facts     ── Assemble: PR metadata, diff stats, per-file classification
        │                (internal/classes.Classify: review-config → ci-config →
        │                 deps → generated → docs → test → source, first match wins)
        ▼
internal/gate       ── Skip: CEL-based skip_classes / skip_when (facts-only) ─┐
        │ (not skipped)                                                       │ (skipped)
        ▼                                                                     ▼
internal/runner.RunTriage  ── exactly one "triage"-kind persona            zero-token
        │  (risk/complexity/domains → schema/triage/v1)                    summary, exit
        ▼
internal/roster.Compute  ── 8-step deterministic team assembly:
        │   required → volunteers → excludes → cap(max) → floor →
        │   escalation → verifier lenses → budget enforcement
        │   (internal/activation.Evaluate runs CEL rules from personas/*.yaml)
        ▼
internal/runner.RunTeam  ── concurrent reviewer personas (wave 1 + escalation wave 2)
        │   agent personas: multi-turn tool-calling loop → internal/infer.Client
        │   deterministic personas: internal/handlers (dep-risk via OSV/deps.dev,
        │   config-guard) — zero or one model call, no tool loop
        ▼
internal/verify.Run  ── fixed lens order: groundedness → injection → duplication →
        │                materiality (internal/verify/*.go), each may call a
        │                "verifier" persona (personas/verifier/*.yaml)
        ▼
internal/render + internal/post  ── render finding/summary comments with dedup
        │                            markers, upsert via internal/gh.Port
        ▼
internal/gate.Exit  ── exit code from highest surviving severity vs. fail_on
```

Personas are **the** extensibility unit: every reviewer, triage step, and verifier is a
YAML persona (`personas/*.yaml`, `personas/verifier/*.yaml`) with a paired Markdown
system prompt (`prompts/*.md`, `prompts/verifier/*.md`), resolved at config-load time by
`internal/persona.Resolve` (layers: builtins → repo-local `.github/agentic-review/personas/`
→ `config.yaml` overrides). Activation (`required_when` / `volunteer_on` / `always`) is
CEL, evaluated against a frozen `facts` (+ optionally `assessment`) namespace by
`internal/activation`.

## Key Directories

- `cmd/agentic-review/` — CLI entry point and subcommands: `main.go` (dispatch),
  `review.go` (Action entrypoint, full pipeline), `triage.go` (standalone triage),
  `plan.go` (tier-0 + roster only, zero model calls), `validate.go`, `fetch.go`
  (download a prior run's artifacts from a GitHub Actions run).
- `internal/` — ~25 single-purpose packages; notable ones:
  - `runner/` — orchestration: `review.go`, `triage.go`, `team.go`, `mechanical.go`.
  - `persona/` + `activation/` — persona registry resolution and CEL activation rules.
  - `roster/` — deterministic team-assembly algorithm (`roster.go:Compute`).
  - `infer/` — OpenAI-compatible inference client (`client.go`), request/response
    types (`types.go`), and record/replay (`record.go`) for deterministic testing.
  - `verify/` — the four fixed verification lenses plus fingerprinting.
  - `gh/` — GitHub access `Port` interface (`port.go`) with a real implementation
    (`github.go`) and a fixture-backed `Fake` (`fake.go`) for tests/dry-run.
  - `classes/` — file classifier + per-ecosystem manifest parsers
    (`classes/manifest/`: npm, cargo, gomod, deno).
  - `schema/` — Go types + JSON Schemas for `triage/v1`, `findings/v1`, `verdicts/v1`.
  - `config/`, `localconfig/` — `config.yaml` struct/defaults and repo-local loading.
  - `render/`, `post/`, `gate/` — comment rendering, GitHub posting, exit-code gating.
  - `goldentest/` — shared golden-file test helper (see Testing & QA).
- `personas/` — builtin persona definitions (`logic.yaml`, `triage.yaml`,
  `security.yaml`, `dep-risk.yaml`, `config-guard.yaml`, `fork-guard.yaml`,
  `verifier/{groundedness,injection,duplication,materiality}.yaml`).
- `prompts/` — Markdown system prompts, one per persona, same naming/nesting as
  `personas/` (e.g. `personas/logic.yaml` ↔ `prompts/logic.md`).
- `fixtures/` — golden/replay test fixtures: `classes/` (classifier corpus per
  ecosystem), `roster/` (facts+config → roster cases), `replay/` (full pipeline replay
  with recorded model I/O), `diffs/` (raw diffs), `cli-smoke/` (CLI + shim smoke fixture).
- `shim/index.mjs` — zero-dependency Node 24 GitHub Action entrypoint (`action.yml`
  `runs.main`); resolves and execs the Go binary.
- `docs/` — `spec.md` (authoritative v1 design, §1–§16) and `setup.md` (deployment
  runbook).
- `.github/agentic-review/` — this repo's own dogfood config for the action
  (`config.yaml`) — used by `dogfood.yml` and `just validate`.

## Development Commands

Tool versions are pinned in `.mise.toml` (go 1.26.6, node 24, just, golangci-lint 2). Use
`just` for everything; see `justfile` for the exact recipes:

```bash
just build        # go build -o agentic-review ./cmd/agentic-review
just test          # go test ./...
just test-update    # go test ./internal/classes ./internal/roster ./internal/runner -update
                     # (only these 3 packages import internal/goldentest)
just lint          # gofmt -l check + go vet ./... + golangci-lint run (no auto-fix)
just check         # lint + test — what CI runs
just validate       # go run ./cmd/agentic-review validate — loads every builtin +
                     # repo-local persona and config.yaml, fails on any invalid definition
just smoke          # CLI smoke (plan subcommand, no models) + shim smoke
                     # (shim/index.mjs with AGENTIC_REVIEW_DRY_RUN=1, no network)
just live-smoke      # only runs if AGENTIC_REVIEW_ENDPOINT/AGENTIC_REVIEW_MODEL set;
                     # otherwise prints a notice and exits 0 — never fails CI
just clean          # rm -rf agentic-review dist .agentic-review
```

CI (`.github/workflows/ci.yml`) runs `just check` then cross-compiles
(linux/amd64, linux/arm64, darwin/arm64). `dogfood.yml` self-reviews every PR with the
in-flight binary via `uses: ./`. `release.yml` publishes cross-compiled binaries +
`checksums.txt` on `v*` tags.

## Code Conventions & Common Patterns

- **Deterministic shell, agentic core.** Routing, roster computation, verification
  merging/gating, and posting are pure functions/deterministic Go — never delegate
  control flow to a model. Only persona execution and specific verifier lenses
  (`groundedness`, `materiality`) make model calls; `duplication` and `injection` (on
  non-fork PRs) are largely mechanical.
- **Everything is a persona.** New reviewer behavior is added as a
  `personas/<name>.yaml` + `prompts/<name>.md` pair, not new Go code, unless the
  persona is `kind: deterministic` (a Go handler in `internal/handlers/`, e.g.
  `deprisk.go`, `configguard.go`).
- **Model output is untrusted input.** Every model response is schema-validated
  (`santhosh-tekuri/jsonschema`), evidence must byte-match the diff (mechanical
  validation in `runner/mechanical.go`), and findings are re-verified through fixed
  lenses before posting. Untrusted PR content (title/body/commits) is wrapped via
  `infer.WrapUntrusted()` before being placed in model context.
- **Facts vs. judgment are structurally separated.** `internal/facts.Facts` is
  computed once, deterministically, and CEL rules under `skip_when`/`required_when`
  that only have `facts` available are lint-checked (`internal/activation`) to reject
  any reference to `assessment` — see `internal/activation/lint_eval_test.go`.
- **Interface boundary for external I/O:** `internal/gh.Port` is the single interface
  for all GitHub access; `github.go` is the real `go-github`-backed implementation,
  `fake.go` is a fixture-backed test/dry-run double. New GitHub calls go through
  `Port`, never a bare `*github.Client` outside `internal/gh`.
- **Determinism in inference:** `internal/infer.Client.Complete` pins
  `Temperature=0`, `TopP=1`, `Seed=1`. `internal/infer/record.go` provides
  Recorder/Replayer wrappers (`Select()` picks Replayer > Recorder > base Client)
  so pipeline tests never require a live endpoint.
- **Config loading is additive, not wholesale.** `internal/config.Load` fills every
  unset field from `Defaults()` independently (not a struct-level fallback), and
  parses with `goccy/go-yaml` `Strict()` mode (unknown fields are a load error).
- **Logging** goes through `internal/logx` using GitHub Actions workflow commands
  (`::warning::`, `::error::`, `::debug::`); `Debug()` is suppressed outside Actions
  unless `AGENTIC_REVIEW_DEBUG=1`.
- **Error handling** uses `errors.Is`/`errors.As` for typed sentinel/wrapped errors
  (e.g. `runner.ErrTriageFailed`, `infer.ErrNoRecording`); `errorlint` is enforced by
  `.golangci.yml`.
- **Testing conventions:** table-driven tests, `google/go-cmp` for diffing, and a
  shared golden-file helper (`internal/goldentest`) — see Testing & QA below.

## Important Files

- `cmd/agentic-review/main.go` — subcommand dispatch (`review`, `plan`, `triage`,
  `validate`, `fetch`).
- `internal/runner/review.go` — the single orchestration entrypoint tying the whole
  pipeline together (`Review()`); `ReviewDeps` bundles all injected dependencies
  (`gh.Port`, `infer.Client`, OSV/deps.dev clients, config paths, dry-run/record flags).
- `internal/config/config.go` / `internal/config/load.go` — `Config` struct and
  `config.yaml` schema (team sizing, skip rules, gate thresholds, per-severity caps,
  `models:` capability bindings).
- `internal/localconfig/localconfig.go` — resolves `.github/agentic-review/` (config +
  repo-local personas) against builtins.
- `internal/persona/definition.go` — `ResolvedPersona`, hard ceilings
  (`MaxTeamSize=8`, `MaxTotalTokens=400k`, `MaxToolCallsPerPersona=25`, etc.).
- `internal/schema/types.go` — `Assessment` (triage/v1), `Payload`/`Finding` (findings/v1).
- `assets.go` — root-package `//go:embed personas prompts` bridging Go's embed
  restriction so builtin personas/prompts ship inside the binary.
- `action.yml` — GitHub Action definition (inputs: `github_token`, `config_path`,
  `endpoint`, `api_key`, `record`, `fail_on`; outputs: severity counts, `tokens`, `roster`).
- `shim/index.mjs` — binary resolution ladder (env var → baked `/usr/local/bin` →
  `$RUNNER_TOOL_CACHE` → GitHub Release download w/ SHA256 check), each rung
  version-checked via `<bin> version` before trust.
- `docs/spec.md` — authoritative behavioral spec; cite the relevant `§N` section in
  commits/PRs that change pipeline behavior.
- `docs/setup.md` — inference endpoint requirements (OpenAI-compatible
  `/v1/chat/completions`, `reasoning_effort: none` to avoid billing reasoning tokens as
  completion tokens), self-hosted runner + egress allowlist, mandatory `models:` block.

## Runtime/Tooling Preferences

- **Go 1.26.6** (see `.mise.toml`; `go.mod` requires `go 1.26.0`). Module path:
  `github.com/phatblat/agentic-review`.
- **Node 24** only for the Action shim (`shim/index.mjs`) — zero npm dependencies by
  design; do not add a `package.json`/`node_modules` dependency to the shim.
- **`just`** is the canonical task runner; prefer `just <recipe>` over raw `go`/shell
  invocations so behavior matches CI.
- **`golangci-lint` v2** with an explicit allowlist (`linters.default: none`, then
  `enable:` errcheck, govet, ineffassign, staticcheck, unused, bodyclose, errorlint,
  misspell). `gofmt`/`goimports` are formatters, not auto-applied by `just lint`.
- Notable direct dependencies and their purpose: `google/cel-go` (persona activation
  CEL rules), `google/go-github/v75` (GitHub API), `goccy/go-yaml` +
  `pelletier/go-toml/v2` (config/persona parsing), `santhosh-tekuri/jsonschema/v6`
  (schema validation of model output), `bmatcuk/doublestar/v4` (glob matching for
  `touches()`/docs globs), `tailscale/hujson` (JSONC parsing), `Masterminds/semver/v3`
  (dependency bump-level detection), `google/go-cmp` (test diffing only).

## Testing & QA

- `go test ./...` (`just test`) is the full suite; no build tags or network access
  required for the default suite.
- **Golden-file tests** via `internal/goldentest` (registers `-update` at package
  init). Only `internal/classes`, `internal/roster`, `internal/runner` import it —
  `just test-update` runs `go test` scoped to exactly those three packages. Pattern:
  compare marshaled JSON against a `want*.json` fixture with `google/go-cmp`; rerun
  with `-update` to rewrite the fixture after an intentional behavior change.
- **Fixture-driven cases** live under `fixtures/`:
  - `fixtures/classes/<ecosystem>/<case>/{base,head}/<file>` + `want.json` — classifier
    corpus (`internal/classes/golden_test.go`).
  - `fixtures/roster/<case>/{facts.json, assessment.json?, config.yaml}` +
    `want-roster.json` — roster computation (`internal/roster/golden_test.go`).
  - `fixtures/replay/security-token-expiry/` — full pipeline replay: `pr.json`,
    `files.json`, `event.json`, `base/`/`head/` trees, recorded model
    request/response pairs under `recordings/`, and `want-*.json` expected output
    (`internal/runner/replay_test.go`).
  - `fixtures/diffs/` — raw unified diffs for CLI/triage tests.
  - `fixtures/cli-smoke/high-risk-auth/` — used by `just smoke`.
- **`internal/gh.Fake`** is the fixture-backed double for `gh.Port` — reads
  `pr.json`/`files.json`/`comments.json`/etc. from a directory, file content from
  `base/<path>`/`head/<path>` on disk, and records every mutating call
  (`PostedCall`) for assertion. Used across `runner`, `post`, and dry-run mode
  (`AGENTIC_REVIEW_DRY_RUN=1`).
- **Record/replay for inference**: `internal/infer/record.go` — `Recorder` persists
  every request/response keyed by a canonical hash (excludes `seed`/timeouts);
  `Replayer` reads them back for deterministic tests without a live endpoint.
- **Smoke tests** (`just smoke`) are model-free and network-free: CLI `plan`
  subcommand against a pre-computed triage assessment, plus a shim smoke test that
  exercises `shim/index.mjs`'s binary-resolution fallback (asserts on the literal
  `"falling through"` log line when a candidate binary fails its version check).
- **Live smoke** (`just live-smoke`) only runs against a real inference endpoint
  when `AGENTIC_REVIEW_ENDPOINT`/`AGENTIC_REVIEW_MODEL` are set; otherwise it prints
  a skip notice and exits 0, so it never blocks CI without deployment credentials.
- `just validate` is a lightweight non-model check that every builtin and
  repo-local persona plus `config.yaml` loads and resolves cleanly (capability
  bindings exist, CEL compiles, no duplicate/invalid persona ids).
