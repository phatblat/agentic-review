# agentic-review — System Specification

**Status:** v1 design, ready for implementation
**Predecessor:** github.com/phatblat/claude-code-review (prototype)

---

## 1. Overview

`agentic-review` performs variable-sized code review on GitHub pull requests using
locally hosted models on self-hosted runners. Review effort scales with the size and
complexity of the change: trivial changes (dependency bumps, docs) exit through
deterministic classification with zero model calls; complex changes assemble a
configurable team of reviewer personas, whose findings pass through verification
before posting.

Design principles:

1. **Deterministic shell, agentic core.** All orchestration, roster computation,
   validation, comment posting, and gating is deterministic Go code. Models make
   judgment calls only, always under structured output constraints.
2. **Everything is a persona.** Deterministic checks, LLM reviewers, verifiers, and
   triage share one registry format, one activation model, one output contract.
3. **Model output is untrusted input.** Schema-validated, evidence-checked, verified,
   and posted by deterministic code holding a minimally scoped token.
4. **Facts vs. judgment.** Code-computed facts and model-emitted assessments are
   structurally separated everywhere (triage, findings). Security-relevant decisions
   key on facts only.
5. **The binary is the product.** The Go binary is the CLI, the test harness, and the
   action core. Wrappers are packaging.

---

## 2. Architecture

### 2.1 Pipeline

```
event → tier 0 (deterministic classify) ──skip──→ post summary, exit
                    │
                    ▼
        tier 1 (triage persona, small model) → triage.json
                    │
                    ▼
        roster computation (CEL activation over triage.json) → roster.json
                    │
                    ▼
        tier 2 (persona team, parallel where possible) → findings.raw.json
                    │
                    ▼
        verification (lenses) → verdicts.json → findings.final.json
                    │
                    ▼
        dedup, caps, render → review comments + summary comment → gate (exit code)
```

### 2.2 Tier 0 — deterministic gate

Pure code. Computes `facts` (see §6.1) including diff classes. Skip decisions
(`skip_when` rules) may reference **facts only** — a model assessment can shrink a
team but can never zero it out. Default skip classes: `deps`, `docs` (configurable).
A skipped review still posts the summary comment ("skipped agentic review:
deps-only change") with a zero-token footer.

### 2.3 Tier 1 — triage

Exactly one active persona of `kind: triage`. Emits `assessment` under guided
decoding (§6.1). If triage output fails schema validation after N retries (default 2),
the run falls back to the **conservative roster**: `logic`, `security`,
`verifier/groundedness` — fail closed, never fail open.

### 2.4 Tier 2 — team

Roster computed deterministically from facts + assessment + declarative activation
rules (§5). Personas run with scoped inputs and budgets, emit findings under guided
decoding, findings pass through verifier lenses (§7).

---

## 3. Persona registry

### 3.1 Layers

1. **Builtin** — ships with the binary. See §14 for the v1 roster.
2. **Repo-local** — `.github/agentic-review/personas/*.yaml` and
   `.github/agentic-review/config.yaml`.

Resolution: repo-local overrides builtin by `id`, except:

- Builtins may set `immutable: true`. **Only builtins may set this.** Immutable
  personas cannot be disabled, replaced, deprioritized, budget-shrunk, or overlaid by
  any config layer. Their activation is evaluated by the runtime regardless of config.
  v1 immutable builtins: `config-guard`, `verifier/injection` (when fork rules apply).
- Prompt **overlays** (append-only) are permitted where `overlays_allowed: true`.
  Repo config may never replace a builtin's system prompt. Full custom prompts
  require a new repo-local persona id, which is visible in the roster.

Config loads from the **PR head ref**. Safety rests on the structural properties in
§12.4, not on base-ref loading.

### 3.2 Persona ids

Slash-namespaced strings: `security`, `verifier/groundedness`, `fork-guard`.
Charset: `[a-z0-9-]` segments joined by `/`.

### 3.3 Definition format

```yaml
id: security
kind: agent                      # agent | deterministic | verifier | triage
summary: >
  Reviews changes touching auth, secrets handling, input parsing, and network
  boundaries for security regressions.

activation:
  volunteer_on:                  # OR'd trigger groups; keys within a group AND'd
    - paths: ["**/auth/**", "**/*.tf"]
    - domains: [network, secrets]
    - expr: 'facts.deps.changed.exists(d, d.bump == "major")'
  required_when: 'assessment.risk >= RISK_HIGH'
  priority: 90                   # tiebreak when volunteers exceed team cap
  excludes: []                   # suppressed personas; required_when beats excludes

model:
  capability: review             # open string, bound in deployment map (§10.2)
  min_context: 32k
  structured_output: findings/v1

inputs:
  scope: matched-files           # matched-files | full-diff | full-files | metadata-only
  context: [pr-metadata, file-contents-head]
  tools: [read_file]             # explicit allowlist; default none; runtime-mediated

prompt:
  system: prompts/security.md
  overlays_allowed: true

output:
  schema: findings/v1
  severities: [blocker, error, warning, nit]
  max_findings: 15               # blockers exempt from truncation

budget:
  max_tokens: 40000
  max_tool_calls: 10

verification:
  required: true                 # deterministic personas may set false
  lenses: [groundedness, materiality]
```

Deterministic personas replace `model`/`prompt` with:

```yaml
runtime:
  handler: builtin/dep-risk      # v1: builtin/* only; handler URI scheme reserved
                                 # for future wasm/ (see §15)
```

Verifier personas add `lens: groundedness | materiality | duplication | injection`.
Reviewers reference lenses, not verifier ids, so implementations are swappable.

### 3.4 Team shaping

```yaml
review:
  team: { min: 1, max: 5 }
  escalation:
    - { when: 'facts.diff.additions > 500', add: [architecture] }
  gate: { fail_on: nit }         # nit = any surviving finding fails (default)
  caps: { nit: 5, warning: 10, error: 20 }   # per-severity comment caps; blockers uncapped
  verification:
    materiality_floor: downgrade # downgrade | drop (fork PRs force drop)
```

Volunteer overflow: sort by `priority`, keep all `required_when` matches
unconditionally. **Escalations** (from `escalation` rules, persona `escalate`
outputs, or triage `suggested_personas`) are additive-only, restricted to personas
already permitted by config, within remaining budget, and always logged via
`::warning::`. Denied escalations (budget) are logged as denied.

---

## 4. Diff classes and detectors

Enum: `source | test | docs | deps | generated | ci-config | review-config`

All detectors are **conservative**: a class is assigned only on positive proof;
anything unrecognized is `source` (full review). Each detector is a pure function
with a golden-fixture corpus (`fixtures/classes/<ecosystem>/<case>/`).

- **`deps`** — every changed path is (a) a known lockfile: `Cargo.lock`,
  `package-lock.json`, `pnpm-lock.yaml`, `yarn.lock`, `bun.lockb`, `bun.lock`,
  `deno.lock`, `go.sum`, `Gemfile.lock`, `poetry.lock`, `uv.lock` (path match only,
  never content-parsed — `bun.lockb` is binary); or (b) a manifest whose
  **structural diff** (ecosystem parser: TOML for Cargo, JSON for package.json, JSONC
  for deno.json) is confined to version fields (dependency version specs or the
  package's own version). Deno: only the version portion of specifier strings in the
  `imports` map. Text diffs are never trusted. Unknown format ⇒ not `deps`.
  v1 ecosystems: cargo, npm/pnpm/yarn/bun, deno, go.
- **`docs`** — every path matches the docs glob set (default:
  `**/*.md`, `docs/**`, `**/*.rst`, `LICENSE*`; repo-extendable — extensions are
  themselves review-config and thus under config-guard).
- **`generated`** — `.gitattributes linguist-generated` authoritative when present;
  else marker heuristic (`DO NOT EDIT`, `@generated` in first 20 lines) plus default
  globs (`*.pb.go`, `dist/**`). Heuristic assignments log their reason.
- **`review-config`** — any path under `.github/agentic-review/**` or the workflow
  file invoking the action. Sets `facts.diff.touches_review_config`.
- **`ci-config`** — `.github/workflows/**`, CI config files.
- **`test`** — conventional test globs per ecosystem.

---

## 5. Activation grammar

**CEL (cel-go)** over a frozen, versioned namespace. Structured YAML triggers are
sugar compiled to CEL; one evaluator. The compiled expression for every rule appears
in plan output and `roster.json`.

### 5.1 Namespace (defined by triage/v1)

- `facts.pr.*`, `facts.diff.*`, `facts.deps.*`
- `assessment.*` (unbound on triage failure; rules referencing it are not-matched)
- Ordered enums as ints with constants: `RISK_LOW..RISK_CRITICAL`,
  `COMPLEXITY_TRIVIAL..COMPLEXITY_COMPLEX`, `ASSOC_OWNER`, `ASSOC_MEMBER`,
  `ASSOC_COLLABORATOR`, `ASSOC_CONTRIBUTOR`, `ASSOC_FIRST_TIME_CONTRIBUTOR`, `ASSOC_NONE`

No env, no I/O, no `now()`. CEL cost budget set low; parse + type-check every rule in
every layer at config load. Any error fails the run before models start.
`agentic-review validate` performs the same check locally.

### 5.2 Function library (closed, pure)

| Function | Meaning |
|---|---|
| `touches(glob)` | any changed path matches |
| `touches_only(glob...)` | every changed path matches one of |
| `added_over(n)` / `files_over(n)` | size shorthands |
| `has_class(c)` | `c in facts.diff.classes` |
| `dep_bumped(ecosystem, level)` | any dep change at ≥ semver level |

### 5.3 Sugar mapping

`paths:` → `touches(...) || ...` · `languages:` → `"x" in facts.diff.languages` ·
`domains:` → `assessment.domains.exists(d, d in [...])` · `labels:` → membership on
`facts.pr.labels` · `expr:` → raw CEL inline.

### 5.4 Context classes (load-time lint, AST-walk enforced)

| Slot | May reference |
|---|---|
| `skip_when` | `facts.*` only |
| `required_when` on immutable builtins | `facts.*` only |
| `volunteer_on`, escalation `when`, other `required_when` | `facts.*` + `assessment.*` |

Violations are load errors. Plan output tags each rule with its context class.

---

## 6. Schemas

### 6.1 triage/v1

```jsonc
{
  "schema": "triage/v1",
  "facts": {                       // assembled by the runtime, pre-model
    "pr": { "number": 481, "base_ref": "main", "head_sha": "…",
            "is_fork": true, "author_association": "FIRST_TIME_CONTRIBUTOR",
            "labels": ["deps"], "draft": false, "commits": 3 },
    "diff": { "files_changed": 14, "additions": 380, "deletions": 42,
              "languages": { "rust": 310, "yaml": 70 },
              "paths": ["src/auth/token.rs"],
              "classes": ["source", "ci-config"],
              "touches_review_config": false, "touches_workflows": true,
              "binary_files": 0, "max_file_additions": 122 },
    "deps": { "changed": [ { "ecosystem": "cargo", "name": "openssl",
                             "from": "1.0.2", "to": "3.2.1", "bump": "major" } ] }
  },
  "assessment": {                  // triage model, guided decoding; advisory
    "risk": "high",                // low | moderate | high | critical
    "complexity": "moderate",      // trivial | simple | moderate | complex
    "domains": ["auth"],           // closed enum, §6.3
    "summary": "…", "rationale": "…",
    "suggested_personas": ["security"],   // advisory; escalation rules apply
    "confidence": 0.82
  }
}
```

### 6.2 findings/v1

A finding is a **model-emitted payload** inside a **runtime-stamped envelope**.
Nothing model-emitted can forge provenance or verdicts.

```jsonc
{
  "schema": "findings/v1",
  "payload": {
    "category": "security",        // §6.3
    "severity": "error",           // blocker | error | warning | nit
    "title": "Token expiry check removed",
    "claim": "…",
    "domains": ["auth"],           // optional; same enum as triage domains
    "anchor": { "kind": "line",    // line | file | pr
                "path": "src/auth/token.rs",
                "start_line": 84, "end_line": 91,
                "ref": "head" },   // head | base (base = deleted code)
    "evidence": [ { "path": "src/auth/token.rs",
                    "start_line": 84, "end_line": 86,
                    "source": "let claims = decode_unverified(&token)?;" } ],
    "suggested_fix": { "start_line": 84, "end_line": 86, "replacement": "…" },
    "confidence": 0.85
  },
  "envelope": {
    "id": "f-0007",
    "fingerprint": "sha256:…",
    "persona": "security", "persona_kind": "agent",
    "model": "review/qwen3-32b@spark",
    "head_sha": "…",
    "verification": {
      "verdicts": [ { "lens": "groundedness", "result": "pass",
                      "checked": "mechanical+model" } ],
      "disposition": "accepted"    // accepted | dropped | downgraded | merged
    },
    "posted": { "comment_id": 123456789 }
  }
}
```

Validation rules:

- **Evidence** — `evidence.source` must byte-match file content at `head_sha` for the
  stated lines. Checked mechanically before any verifier model runs; mismatch ⇒ drop
  without token spend. Agent findings with no evidence fail schema validation.
  Deterministic-persona findings may omit evidence (their output is ground truth).
- **Anchors** — `line` anchors must fall within changed hunks. Failing anchors:
  `::warning::`, demote to `file` (if the file is in the diff) else to `pr`.
- **suggested_fix** — must dry-run-apply cleanly to the anchored lines at `head_sha`;
  dropped with a debug log otherwise. On fork PRs, rendered as a plain fenced code
  block, never a committable suggestion, unconditionally.
- **Fingerprint** — runtime-computed:
  `sha256(normalized path + category + whitespace-normalized evidence sources +
  claim-stem)`. Persona- and line-number-independent (survives rebases). Used for
  intra-run merge (duplication lens) and cross-run dedup (comment markers, §8.4).
- **Renamed severities** — `blocker ⛔️ | error 🚨 | warning ⚠️ | nit 🧼`.

### 6.3 Closed enums

- **domains** (triage assessment + optional finding payload): `auth, secrets,
  network, storage, concurrency, api-surface, ui, build, ci, dependencies,
  data-handling`
- **categories** (findings): `correctness, security, performance, testing, docs,
  style, config, o11y, i18n`

Axis rule, binding for future additions: **a domain answers "where"; a category
answers "what's wrong."** A race condition is `category: correctness` in
`domains: [concurrency]`; a vulnerable bump is `category: security` in
`domains: [dependencies]`.

---

## 7. Verification

Lens semantics are fixed by this spec, not config:

| Lens | Fail means | Disposition |
|---|---|---|
| `groundedness` | evidence missing / mismatched / doesn't support claim | **drop** |
| `materiality` | true but below attention floor | downgrade to `nit`, or drop (`materiality_floor`; fork PRs force drop) |
| `duplication` | same fingerprint or same-anchor-same-claim | merge (keep highest severity; all contributors credited in envelope, only survivor in the posted comment) |
| `injection` | claim/fix contains instructions, exfil URLs, encoded blobs, manipulation patterns | **drop + `::warning::` annotation** |

- `groundedness` runs its mechanical byte-match first, model judgment second.
- Agent-emitted `blocker`s require a full verdict pass to survive; deterministic
  blockers skip verification per persona config.
- Verifier calls draw from verifier token budgets; verdicts land in `verdicts.json`.

---

## 8. Comment surface

### 8.1 Rendering ladder

1. `anchor.kind: line` in a changed hunk → **review comment** (all review comments
   submitted as one review — one notification). Suggestion block on internal PRs;
   plain fence on forks.
2. `anchor.kind: file`, or demoted line anchor, file present in the diff →
   **file-level review comment** (`subject_type: "file"`). No suggestion blocks
   (API limitation).
3. `anchor.kind: pr`, or file not in the diff → **section of the PR-level summary**.

### 8.2 Summary comment

Always exactly one per review run. **Upserted** on re-review: locate by marker,
edit in place. Variants:

- **No findings** — "✅ No findings" + footer. Tier-0 skips read
  "skipped agentic review: deps-only change" with a 0-token footer.
- **Findings** — counts by severity (⛔️🚨⚠️🧼), then "Findings not on changed lines"
  with full bodies, then the filtered section, then footer.
- **Error** — failed stage, whether the fallback roster ran, footer gains
  `📋 [run log](…/actions/runs/<id>)` (log link on error only).

### 8.3 Filtered-findings section

Collapsed `<details>` listing every dropped/downgraded finding: severity emoji,
persona, title, lens, verdict reason. **Injection-dropped findings render verdict
metadata only — claim content is withheld** ("*(content withheld)*") to avoid
republishing attacker payloads where maintainers read and copy. Full content remains
in `findings.raw.json` in the run artifact.

### 8.4 Marker grammar

Every action-created comment begins with exactly one HTML comment on line 1:

```
<!-- agentic-review/1 kind=summary run=17283 status=findings history=<urlencoded-json> -->
<!-- agentic-review/1 kind=finding fp=sha256:… run=17283 seq=3 persona=security sev=error -->
<!-- agentic-review/1 kind=ack run=17284 in-reply-to=comment:456123 -->
```

`agentic-review/<marker-version>` + space-separated `key=value` pairs, values
URL-encoded. `kind ∈ {summary, finding, ack}`. Marker version is independent of
schema versions. Uses: self-identification (summon skip-fast), "already reviewed"
detection (presence of a `kind=summary` marker), cross-run finding dedup (`fp`),
enumeration (`run` + `seq`), and run history (below).

### 8.5 Footer and run history

Footer fields: roster with resolved model per member, total tokens (0 for pure
deterministic passes), wall-clock runtime, log link on error. Single run:

```
👥 triage (qwen3-8b) · logic (qwen3-32b) · security (qwen3-32b) · dep-risk (deterministic) · verifier/groundedness (qwen3-8b)
🔢 41,203 tokens · ⏱ 2m 14s
```

On re-review the footer becomes a **runs table** (latest first): run link, trigger
(`opened`, `/agentic-review`, `retarget`), team, tokens, runtime. Machine state for
the table is the URL-encoded JSON `history` field in the summary marker; each upsert
appends to it and re-renders the table — history survives human edits to the body
and is never parsed back from rendered markdown.

### 8.6 Caps

Per-severity comment caps from config (`caps:`); blockers uncapped and exempt from
`max_findings` truncation. Suppressed counts reported in the job summary and the
PR summary footer ("7 findings suppressed by cap").

---

## 9. Gate and exit codes

| Exit | Meaning |
|---|---|
| 0 | clean (no finding ≥ `fail_on` survived) |
| 1 | infra/config failure (no judgment implied) |
| 2 | findings at or above `gate.fail_on` survived |

`fail_on` values follow the severity enum; default `nit` (any surviving finding
fails). "Pass on nits-only" = `fail_on: warning`. On PRs where
`facts.diff.touches_review_config` is true, the run cannot exit 0 with unresolved
`config-guard` blockers (see §12.4).

---

## 10. Inference

### 10.1 Client contract

OpenAI-compatible `/v1/chat/completions`. Structured output via `response_format`
json_schema — the sole structured-output field the request body carries; no
vLLM `guided_json` extra is sent alongside it. (A separately operated bare
vLLM endpoint that only accepts `guided_json` would need an explicit endpoint
dialect setting in a future change; v1 emits `response_format` unconditionally
and never both.) Schemas are compiled from `triage/v1`, `findings/v1`,
`verdicts/v1`. Persona tools (`read_file`, `osv_lookup`) use the tool-calling
API with the loop owned by the Go harness; tools are runtime-mediated
capabilities, never direct model network access.

Deployment note (Qwen on vLLM): enable `--enable-auto-tool-choice
--tool-call-parser hermes` for parseable tool calls.

### 10.2 Capability classes and binding

Classes are open strings; builtins use three conventions: `triage`, `review`,
`verify`. The deployment map must define every class referenced by any active
persona (load-time check):

```yaml
models:
  triage: { endpoint: "http://spark:8000/v1", model: "qwen3-8b" }
  review: { endpoint: "http://spark:8000/v1", model: "qwen3-32b" }
  verify: { endpoint: "http://spark:8000/v1", model: "qwen3-8b" }
```

Budgets are tokens-only in v1. Hard ceilings (max team size, max tokens/PR, max tool
calls) are baked into the binary and enforced independent of repo config; repo config
may lower, never raise. Rate limits at the inference gateway are the recommended
second layer (outside the blast radius of any repo content).

### 10.3 Throughput and prompt caching

The deployed inference service caps throughput at 500k tokens/minute (TPM) and
120–200 requests/minute (RPM). TPM is the binding constraint for any
substantial persona: an approximately 100k-token persona prompt is limited to
about five requests/minute by TPM (500,000 / 100,000), long before the RPM
ceiling is reached. The tier-2 team's four-persona concurrency semaphore
(§3.4's `runWave`, min(team size, 4)) can legitimately burst several such
requests at once and hit this ceiling; 429 retries (below) provide
backpressure so a burst degrades to bounded latency instead of a hard
failure, but they do not increase the service's actual throughput.

**Retry contract.** A 429 response retries up to three times (four attempts
total). Its `Retry-After` header, when present, sets that attempt's delay —
either an integer count of seconds or an HTTP-date — capped at 60s per sleep;
a missing or malformed header falls back to the fixed `250ms/1s/4s` backoff.
HTTP 5xx and network errors use the same three-retry, four-attempt shape with
the fixed backoff (no `Retry-After` to honor). Every other 4xx response is
immediately fatal, never retried.

**Prompt-cache observability.** The client reads
`usage.prompt_tokens_details.cached_tokens` from every response. `budget.json`
(§13.3) reports a run-level `usage` object — `prompt`, `cached_prompt`,
`completion`, `total` tokens — aggregated across triage, tier-2, and
verification by one live meter per run. Cache hit ratio is
`cached_prompt / prompt` when `prompt` is nonzero. Message ordering keeps the
system prompt first and stable across turns so the deployment's own prefix
cache can key on it; caching is a latency/throughput optimization the client
observes, never a correctness dependency.

**Run attribution.** The client sends `x-session-id` equal to `GITHUB_RUN_ID`
when the process has one (omitted for local CLI usage). It exists solely for
service-side telemetry and run attribution — never as a cache key or any
other correctness input.

---

## 11. Workflow surface

### 11.1 Events

```yaml
on:
  pull_request:
    types: [opened, ready_for_review, reopened, edited]
    branches: [main]              # default-branch targets only in v1
  issue_comment:
    types: [created]
```

- Skip drafts (`draft: true` at `opened`; `ready_for_review` catches them later).
- **Review-once:** `edited`/`reopened` check for an existing `kind=summary` marker
  and exit summarily unless the base branch changed (retarget ⇒ new review).
- No `synchronize` in v1 — pushes don't re-trigger; re-review is on demand.

### 11.2 Summons

`/agentic-review [scope…]` in a PR comment (optional scope args, e.g.
`/agentic-review security`). Implementation:

- Job-level guard (evaluated before runner assignment — no-op comments cost nothing):

  ```yaml
  if: >
    github.event.issue.pull_request &&
    contains(github.event.comment.body, '/agentic-review') &&
    github.event.comment.user.type != 'Bot'
  ```

- Runtime skip-fast: parse comment; exit 0 on own marker or bot author.
- Permission gate: commenter must have write+ (collaborator-permission API).
- Ack: 👀 reaction on the summoning comment; then resolve PR head SHA and review.
- Security properties: `issue_comment` workflows run the workflow file from the
  **default branch** (not PR-editable); comments posted with `GITHUB_TOKEN` do not
  trigger workflows (GitHub's recursion breaker) — the guards above exist so this
  stays safe when v2 moves to an App token.

### 11.3 Token permissions

```yaml
permissions:
  contents: read
  pull-requests: write
```

No PATs, no secrets in persona context.

---

## 12. Security and threat model

### 12.1 Network posture

Self-hosted runners are **pull-based** (outbound long-poll to GitHub); zero inbound
ports, no webhook endpoint. Runner VLAN egress allowlist: GitHub endpoints (per
`api.github.com/meta`) + the inference endpoint; deny-all otherwise. The inference
server is reachable only from the runner VLAN, with an auth token or mTLS between
runner and inference API.

### 12.2 Runner hygiene

Ephemeral runners (`--ephemeral`, fresh container per job). The review job **never
executes repo code** — diffs and files are read via the GitHub API. Code-executing
personas are out of scope for v1 (future: opt-in network-disabled sandbox).
Org setting: require approval for workflow runs from outside collaborators.
Concurrency caps on the runner group.

### 12.3 Prompt injection

PR title, body, and diff are attacker-controlled model input. Containment:

- Personas have no write capability; findings are schema-validated JSON posted by
  deterministic code with the scoped token.
- Egress allowlist bounds exfiltration to GitHub comment bodies; the injection lens
  screens findings for URLs/encoded blobs/instructions.
- Fork PRs: injection lens **mandatory** on all agent personas; `fork-guard`
  volunteers (`facts.pr.is_fork && author_association not in [OWNER, MEMBER,
  COLLABORATOR]`); `suggested_fix` stripped to plain fences; materiality floor forces
  drop. If budget cannot fit mandatory guards, **fail closed** (exit 1,
  "insufficient budget for fork review").
- Injection-dropped content is never re-rendered in comments (§8.3).

### 12.4 Config self-modification

Config loads from the PR head (testability, self-dogfooding). Safety properties:

1. `config-guard` is **runtime-enforced**: activation on
   `facts.diff.touches_review_config` (tier-0 fact), immutable, prompt ships with
   the binary.
2. `config-guard` is hybrid: deterministic rules first (deleted `required_when`,
   raised budget, new overlay on a security-relevant persona, workflow permission
   changes — each a certain, diffable fact emitted as a `blocker`), model judgment
   for intent second.
3. Hard ceilings live in the binary and the inference gateway (§10.2).
4. Gate rule: config-touching PRs cannot pass with unresolved config-guard blockers.
5. Fork approval gate remains the outer wall.

Residual risk (named): config-guard's model judgment can be fooled by a
sufficiently subtle config change; the structural properties above carry the
security load.

---

## 13. Packaging, CLI, artifacts, testing

### 13.1 Packaging

- **Go binary** (cel-go), subcommands: `review`, `plan`, `triage`, `validate`,
  `fetch`. Released per-platform with SHA-256 checksums.
- **node24 shim** (marketplace wrapper, ~100 lines): input plumbing, binary
  resolution, exit-code mapping. Resolution ladder:
  `AGENTIC_REVIEW_BIN` env → baked path (`/usr/local/bin/agentic-review`) →
  tool-cache → download from GitHub Releases with pinned checksum.
  Baked binaries are `--version`-checked against the shim's expected version;
  mismatch falls through to download (baking is an optimization, never a
  correctness dependency). git-LFS is not usable for action payloads (runner tarball
  fetch does not resolve LFS pointers).
- **Raw invocation is first-class**: a plain `run: agentic-review review` step
  against a baked binary is the documented on-prem path. Runner-image bake snippet
  (lives in user infra, not a separate repo):

  ```dockerfile
  ARG AR_VERSION=v0.x.y
  ADD --checksum=sha256:… \
    https://github.com/phatblat/agentic-review/releases/download/${AR_VERSION}/agentic-review-linux-arm64 \
    /usr/local/bin/agentic-review
  ```

- IPC: one-shot subprocess per subcommand; logs on stderr/stdout (workflow commands
  `::warning::`/`::debug::` and `$GITHUB_STEP_SUMMARY` written directly by Go);
  structured results as files under `$RUNNER_TEMP/agentic-review/`.

### 13.2 CLI

```
agentic-review review   --event $GITHUB_EVENT_PATH [--record recordings/]
agentic-review plan     --triage triage.json --config .        # no model calls
agentic-review triage   --diff pr.diff [--record recordings/]  # live
agentic-review validate                                        # parse/type-check all config
agentic-review fetch <pr|run|job url> [--out fixtures/] [--run <id>]
```

`fetch` resolves PR → review run → artifact via the GitHub API (needs
`actions: read`; honors `GITHUB_TOKEN` / `gh auth`), unpacks to
`fixtures/pr-NNN-run-MMM/`.

### 13.3 Run artifact

`triage.json`, `roster.json` (per-persona activation reasons + compiled CEL +
evaluation trace), `findings.raw.json`, `verdicts.json`, `findings.final.json`,
`budget.json` (allocated vs consumed per persona, plus a run-level `usage`
object — `prompt`, `cached_prompt`, `completion`, `total` tokens, §10.3 —
aggregated by one live meter across triage, tier-2, and verification),
`recordings/` (opt-in via `--record`; raw, **no redaction** — private-repo
content must be cleaned manually before fixture use; public fixtures come
from the action repo's own PRs or synthetic diffs).

### 13.4 Plan visibility

Every run writes the roster table (persona, kind, model, activation reason, budget,
skip reasons) to `$GITHUB_STEP_SUMMARY`. `ACTIONS_STEP_DEBUG=true` surfaces the
verbose trace (`core.debug` equivalent). `plan --from` artifacts replays roster
computation with zero model calls.

### 13.5 Test strategy

1. **Roster golden tests** — `(triage.json, config) → roster.json` pure-function
   fixtures covering every activation rule, priority tiebreak, escalation budget
   case, fork mandatory-lens rule, and context-class lint. Milliseconds, no models.
2. **Recording replay** — record real model request/response pairs; replay in CI to
   test schema validation, verdict handling, dedup, rendering deterministically.
3. **Detector fixtures** — per-ecosystem class-detector corpora (§4).
4. **Live evals** — labeled PR set with expected findings, run as a **scheduled
   workflow on the action repo** against the Spark; drift detection, never a PR gate.
5. **Dogfood loop** — the action repo's PR workflow builds the binary from the PR
   head and sets `AGENTIC_REVIEW_BIN` to it: freshest test path, independent of
   releases and baked images.

---

## 14. Builtin persona roster (v1)

| id | kind | notes |
|---|---|---|
| `triage` | triage | qwen-class small model; emits assessment |
| `logic` | agent | `always: true`; general correctness |
| `security` | agent | volunteers on paths/domains; required at high risk |
| `fork-guard` | agent | volunteers on fork facts; input-channel manipulation review |
| `config-guard` | deterministic+agent hybrid | immutable; required on review-config |
| `dep-risk` | deterministic | OSV/Deps.dev CVE + release-age on `deps` changes |
| `verifier/groundedness` | verifier | mechanical byte-match + model support check |
| `verifier/materiality` | verifier | attention floor |
| `verifier/duplication` | verifier | fingerprint/anchor merge |
| `verifier/injection` | verifier | immutable-mandatory on fork PRs |

Fallback roster (triage failure): `logic`, `security`, `verifier/groundedness`.

---

## 15. Deferred (v2+)

- WASM deterministic handlers (`wasm/` handler scheme; WASI sandbox, host-function
  capabilities) — the `runtime.handler` URI scheme reserves the namespace.
- Org registry layer (repo+ref+path, pinned SHA).
- GitHub App identity: real @-mentionable summons, check-run annotations, posting
  as the app. Requires the §11.2 guards (already built) since App-token comments
  re-trigger workflows.
- Dynamic team assembly: orchestrator model proposes a team from the registry and a
  customizable prompt; deterministic layer enforces min/max, required personas, and
  budget (model proposes, config disposes).
- `synchronize`/delta re-review; non-default-branch targets.
- Wall-clock budgets alongside tokens.
- Code-executing personas in network-disabled sandboxes.
- Non-Linux runner support beyond the shim's resolution ladder.

## 16. Soft defaults (implementer may not change without flagging)

- Summary upsert (not append) on re-review, with marker-carried run history.
- `materiality_floor: downgrade` internal / forced `drop` on forks.
- Default skip classes: `deps`, `docs`.
- Conservative fallback roster as listed.
- Triage retry count N=2 before fallback.
