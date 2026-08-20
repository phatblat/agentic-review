# Deployment setup

Runbook for standing up `agentic-review` on a repository. The authoritative
design is [`spec.md`](spec.md); this document covers only what an operator has
to provision, in the order the pieces depend on each other.

## 1. Inference endpoint

An OpenAI-compatible `/v1/chat/completions` endpoint (spec §10.1). The request
body carries `response_format: {type: json_schema}` as its sole
structured-output field — no `guided_json`, no `extra_body` — plus `model`,
`messages`, `max_tokens`, `temperature`, `top_p`, and `seed`. Auth is
`Authorization: Bearer <key>`. Each request also carries `x-session-id` set to
`$GITHUB_RUN_ID` for service-side attribution only (spec §10.3).

For tool-calling personas on vLLM, deploy with `--enable-auto-tool-choice
--tool-call-parser hermes`.

Verify the endpoint before wiring any workflow:

```sh
AGENTIC_REVIEW_ENDPOINT=http://<host>:<port>/v1 \
AGENTIC_REVIEW_MODEL=<model-name> \
AGENTIC_REVIEW_API_KEY=<key> \
  just live-smoke
```

This runs the triage stage against the real endpoint and asserts a recording
was written. It skips with a notice — never fails — when either
`AGENTIC_REVIEW_ENDPOINT` or `AGENTIC_REVIEW_MODEL` is unset.

## 2. Runner

The review job needs network reach to both `api.github.com` and the inference
endpoint. A VLAN-local endpoint therefore rules out GitHub-hosted runners: use
a self-hosted runner on that VLAN.

- Register per repository (`Settings → Actions → Runners`). Personal-account
  runners cannot be shared across repositories; each consuming repo needs its
  own registration, or the repos need to live in an organization.
- Run ephemeral (`--ephemeral`, fresh container per job) per spec §12.2. The
  review job never executes repository code — diffs and file contents are read
  through the GitHub API — but the runner still handles attacker-controlled PR
  content.
- Egress allowlist: GitHub endpoints (per `api.github.com/meta`) plus the
  inference endpoint. Add `api.osv.dev` and `api.deps.dev` if the repo has
  dependency manifests `internal/classes/manifest` recognizes (`Cargo.toml`,
  `package.json`, `deno.json(c)`, `go.mod`, plus known lockfiles); without
  them the `dep-risk` persona degrades to a single `warning` finding per run
  rather than failing closed.
- **Public repositories:** a self-hosted runner on a public repo lets fork PRs
  attempt to run jobs on your hardware. Set `Require approval for all outside
  collaborators` at minimum. Note also that fork PRs receive a read-only
  `GITHUB_TOKEN`, so `pull-requests: write` is not granted and posting a review
  fails — fork PRs are reviewable in practice only via the `/agentic-review`
  summons path, whose workflow runs from the default branch with a write token.

## 3. Repository config

`.github/agentic-review/config.yaml` is optional in principle — every field
has a default — but `models:` is not: `persona.Resolve` fails closed at load
time unless every capability class referenced by a persona that survives
resolution has a binding (spec §10.2). The builtin roster references `triage`,
`review`, and `verify`, and `fork-guard`/`config-guard`/the verifier lenses
resolve on every run regardless of whether they activate. A repo with no
`models:` block therefore exits 1 with:

```
persona: "fork-guard": capability "review" has no models["review"] binding in config
```

Minimum viable config, with the endpoint left to the workflow:

```yaml
version: 1
models:
  triage:
    endpoint: ""
    model: <model-name>
    context_window: 131072
  review:
    endpoint: ""
    model: <model-name>
    context_window: 131072
  verify:
    endpoint: ""
    model: <model-name>
    context_window: 131072
```

A blank `endpoint` is filled at runtime by the action's `endpoint` input; a
non-blank one always wins over that input. `context_window` is optional (0 =
unknown, which skips the `min_context` fit check); when set it must clear each
bound persona's `model.min_context` — 32k for `logic` and `security`.

Check the result with `agentic-review validate`, which loads config plus every
builtin and repo-local persona and compiles every CEL rule:

```sh
just validate    # in this repo
agentic-review validate /path/to/consuming/repo
```

Repository settings the workflow below expects:

- Variable `AGENTIC_REVIEW_ENDPOINT` — e.g. `http://<host>:<port>/v1`.
- Secret `AGENTIC_REVIEW_API_KEY` — inference API key.

## 4. Consuming-repo workflow

The action's shim resolves a binary through four rungs: `$AGENTIC_REVIEW_BIN`,
`/usr/local/bin/agentic-review`, the tool cache, then a checksum-verified
GitHub Release download. **The release rung is inert until a `v*` tag exists
and its checksums are committed into `shim/index.mjs`**, so build from source
and hand the shim the result via `$AGENTIC_REVIEW_BIN`.

Because the shim version-checks that binary against its own
`EXPECTED_VERSION`, the build stamps the value read out of the checked-out
shim — the two can never drift. Checking the action out locally and using
`uses: ./agentic-review` also guarantees the action and the binary come from
one ref.

```yaml
name: Agentic Review

on:
  pull_request:
    types: [opened, ready_for_review, reopened, edited]
    branches: [main]
  issue_comment:
    types: [created]

permissions:
  contents: read
  pull-requests: write

jobs:
  review:
    if: github.event_name == 'pull_request'
    runs-on: [self-hosted, spark]
    steps:
      # Config loads from the PR head (spec §12.4), not the merge commit.
      - uses: actions/checkout@v7.0.1
        with:
          ref: ${{ github.event.pull_request.head.sha }}

      - uses: actions/checkout@v7.0.1
        with:
          repository: phatblat/agentic-review
          ref: v0.1.0            # pin to a tag or SHA
          path: agentic-review

      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: agentic-review/go.mod

      - name: Build agentic-review
        working-directory: agentic-review
        run: |
          version=$(sed -n 's/^export const EXPECTED_VERSION = "\(.*\)";$/\1/p' shim/index.mjs)
          go build -ldflags "-X main.version=${version}" \
            -o "${RUNNER_TEMP}/agentic-review-bin" ./cmd/agentic-review

      - uses: ./agentic-review
        env:
          AGENTIC_REVIEW_BIN: ${{ runner.temp }}/agentic-review-bin
        with:
          endpoint: ${{ vars.AGENTIC_REVIEW_ENDPOINT }}
          api_key: ${{ secrets.AGENTIC_REVIEW_API_KEY }}

      - uses: actions/upload-artifact@v7.0.1
        if: always()
        with:
          name: agentic-review-run
          path: ${{ runner.temp }}/agentic-review/
          if-no-files-found: ignore

  summons:
    # Job-level guard: evaluated before runner assignment, so no-op
    # comments never occupy the runner (spec §11.2).
    if: >
      github.event_name == 'issue_comment' &&
      github.event.issue.pull_request &&
      contains(github.event.comment.body, '/agentic-review') &&
      github.event.comment.user.type != 'Bot'
    runs-on: [self-hosted, spark]
    steps:
      - uses: actions/checkout@v7.0.1
        with:
          ref: refs/pull/${{ github.event.issue.number }}/head

      - uses: actions/checkout@v7.0.1
        with:
          repository: phatblat/agentic-review
          ref: v0.1.0
          path: agentic-review

      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: agentic-review/go.mod

      - name: Build agentic-review
        working-directory: agentic-review
        run: |
          version=$(sed -n 's/^export const EXPECTED_VERSION = "\(.*\)";$/\1/p' shim/index.mjs)
          go build -ldflags "-X main.version=${version}" \
            -o "${RUNNER_TEMP}/agentic-review-bin" ./cmd/agentic-review

      - uses: ./agentic-review
        env:
          AGENTIC_REVIEW_BIN: ${{ runner.temp }}/agentic-review-bin
        with:
          endpoint: ${{ vars.AGENTIC_REVIEW_ENDPOINT }}
          api_key: ${{ secrets.AGENTIC_REVIEW_API_KEY }}
```

Behavior worth knowing before the first run: draft PRs are skipped at `opened`
and picked up at `ready_for_review`; `edited`/`reopened` exit summarily when a
prior summary comment exists unless the base branch changed; `synchronize` is
not a trigger in v1, so pushes never re-review — use `/agentic-review`. The
job's exit code is 0 clean, 1 infra/config failure, 2 findings at or above
`review.gate.fail_on` (default `nit`; set `fail_on: warning` to pass on
nits-only).

## 5. Releasing (optional)

To replace the build-from-source pre-step with `uses: phatblat/agentic-review@vX`:

1. Tag `vX.Y.Z`. `release.yml` cross-compiles linux/amd64, linux/arm64, and
   darwin/arm64, then publishes `checksums.txt`.
2. Copy those checksums into `CHECKSUMS` in `shim/index.mjs`, keyed
   `<os>-<arch>`, and set `EXPECTED_VERSION` to the *next* tag.
3. Tag again. That second tag is the first one whose download rung works —
   a release cannot contain the checksums of its own artifacts.
