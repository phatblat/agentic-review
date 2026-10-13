# Version source of truth and GoReleaser builds Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add `just version` to print or set the app version everywhere it is written, make plain `go build` report that version, and move the release build into GoReleaser.

**Decision:** 2026-10-12-version-source-of-truth-and-goreleaser-builds — Version source of truth and GoReleaser builds (`docs/decisions/2026-10-12-version-source-of-truth-and-goreleaser-builds.md`)

**Base:** 0cad9fb029776db711478053b9bb303a5249e3de

**Architecture:** The version `vX.Y.Z` lives as a literal in `shim/index.mjs` (`EXPECTED_VERSION`) and `cmd/agentic-review/main.go` (`var version`), pinned together by a Go test, plus the two `ref:` pins in `docs/setup.md`. `just version` rewrites all of them. Non-release builds drop their `sed` + `-ldflags` steps; release builds are defined by `.goreleaser.yaml` and stamped with the tag.

**Tech Stack:** Go 1.26.6, `just` (bash shebang recipes), Node 24 shim, GoReleaser 2.18.3 (via mise), GitHub Actions, actionlint.

**Rules for every task:**

- Run every command from the repository root.
- Never commit, push, stash, switch branches, or write refs or the index; all commits happen after review.
- Recipes must work with both BSD (macOS) and GNU `sed`/`grep`: never `sed -i`.
- Do not touch `internal/classes/manifest/manifest_test.go` or `fixtures/classes/cargo/**`: their `version = "0.1.0"` is a test crate's version, not the app's.

---

### Task 1: Version literal and drift test

**Files:**

- Modify: `cmd/agentic-review/main.go:13-15`
- Test: `cmd/agentic-review/main_test.go`

- [x] **Step 1: Write the failing test**

In `cmd/agentic-review/main_test.go`, replace `import "testing"` with:

```go
import (
	"os"
	"regexp"
	"testing"
)
```

and append:

```go
// TestVersionMatchesShim fails when main.go's version literal and the
// shim's EXPECTED_VERSION drift apart: the shim trusts only a binary whose
// `version` output equals EXPECTED_VERSION, and dogfood.yml and the
// docs/setup.md consumer workflow rely on a plain `go build` passing that
// check. `just version X.Y.Z` rewrites both.
func TestVersionMatchesShim(t *testing.T) {
	src, err := os.ReadFile("../../shim/index.mjs")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^export const EXPECTED_VERSION = "([^"]*)";$`).FindSubmatch(src)
	if m == nil {
		t.Fatal(`shim/index.mjs has no line matching export const EXPECTED_VERSION = "...";`)
	}
	if want := string(m[1]); version != want {
		t.Errorf("main.version = %q, shim EXPECTED_VERSION = %q; run `just version %s`", version, want, want)
	}
}
```

- [x] **Step 2: Run the test to verify it fails**

Run: `go test ./cmd/agentic-review -run TestVersionMatchesShim`
Expected: FAIL with `main.version = "dev", shim EXPECTED_VERSION = "v0.1.0"`

- [x] **Step 3: Set the literal**

Replace `cmd/agentic-review/main.go` lines 13-15 (the comment and `var version = "dev"`) with:

```go
// version is the app version. `just version` keeps it equal to
// shim/index.mjs's EXPECTED_VERSION (TestVersionMatchesShim fails on
// drift), so a plain `go build` passes the shim's version check. Release
// builds stamp the tag over it via .goreleaser.yaml's
// -ldflags "-X main.version={{ .Tag }}".
var version = "v0.1.0"
```

- [x] **Step 4: Run the test and a plain build to verify they pass**

Run: `go test ./cmd/agentic-review`
Expected: PASS

Run: `go build -o /tmp/ar-version-check ./cmd/agentic-review && /tmp/ar-version-check version && rm -f /tmp/ar-version-check`
Expected: prints `v0.1.0`

### Task 2: Plain builds without `sed` or `-ldflags`

**Files:**

- Modify: `justfile:57`
- Modify: `.github/workflows/dogfood.yml:29-38`
- Modify: `docs/setup.md:182-186`, `docs/setup.md:222-227`, `docs/setup.md:267-272`
- Modify: `shim/index.mjs:24-36` (comments only)

- [x] **Step 1: `just smoke` builds plainly**

Replace `justfile` line 57 with:

```just
    go build -o /tmp/agentic-review-shim-smoke ./cmd/agentic-review
```

- [x] **Step 2: dogfood builds plainly**

Replace `.github/workflows/dogfood.yml` lines 29-38 (the comment block and the `run: |` step with `sed` and `go build -ldflags`) with:

```yaml
      # A plain build reports shim/index.mjs's EXPECTED_VERSION:
      # cmd/agentic-review/main.go holds the same literal (`just version`
      # rewrites both; TestVersionMatchesShim fails on drift), so the shim's
      # version check on $AGENTIC_REVIEW_BIN passes (spec §13.1: this rung is
      # a direct override the shim still verifies before trusting it).
      - run: go build -o "${RUNNER_TEMP}/agentic-review-bin" ./cmd/agentic-review
```

- [x] **Step 3: Consumer workflow builds plainly**

Replace `docs/setup.md` lines 182-186 (the paragraph starting "Because the shim version-checks") with:

```markdown
The shim version-checks that binary against its own `EXPECTED_VERSION`, and
`cmd/agentic-review/main.go` carries the same version literal (the action's
own test suite fails if they drift), so a plain `go build` passes. Checking
the action out locally and using `uses: ./agentic-review` also guarantees the
action and the binary come from one ref.
```

Replace each of the two `Build agentic-review` steps (lines 222-227 in `review`, lines 267-272 in `summons`) with:

```yaml
      - name: Build agentic-review
        working-directory: agentic-review
        run: go build -o "${RUNNER_TEMP}/agentic-review-bin" ./cmd/agentic-review
```

- [x] **Step 4: Shim comments are version-agnostic and accurate**

Replace `shim/index.mjs` lines 24-36 (the `EXPECTED_VERSION` comment, the `export const EXPECTED_VERSION` line, and the `CHECKSUMS` comment; keep `export const CHECKSUMS = {` and its body) with:

```js
// EXPECTED_VERSION is the release tag this shim revision was cut for.
// `just version` keeps it equal to cmd/agentic-review/main.go's version
// (TestVersionMatchesShim fails on drift), so a plain `go build`, as in
// dogfood.yml and docs/setup.md's consumer workflow, passes the version
// check below; .goreleaser.yaml stamps release binaries with the same tag.
export const EXPECTED_VERSION = "v0.1.0";

// CHECKSUMS maps "<os>-<arch>" to the sha256 of the EXPECTED_VERSION
// release download for that platform, copied by hand from that release's
// checksums.txt (docs/setup.md section 5). A download attempted while the
// entry for this platform is missing fails loudly rather than skipping
// verification.
```

- [x] **Step 5: Verify**

Run: `just smoke`
Expected: ends with `smoke: OK`

Run: `actionlint .github/workflows/dogfood.yml`
Expected: no output, exit 0

Run: `grep -rn 'main.version=' --include='*.yml' --include='*.md' --include='justfile' . | grep -v -e '^./docs/decisions/' -e '^./PLAN.md:'`
Expected: only `.github/workflows/release.yml` (Task 5 replaces it)

### Task 3: `just version` recipe

**Files:**

- Modify: `justfile` (new recipe immediately above the `# Tag EXPECTED_VERSION …` comment of `release`; `release`'s last `echo`, line 105)
- Modify: `AGENTS.md:124-126` (command block)
- Modify: `docs/setup.md:298-299`

- [x] **Step 1: Add the recipe**

Insert into `justfile` immediately above the line `# Tag EXPECTED_VERSION on clean, up-to-date main and push it to trigger release.yml.`:

```just
# Print the app version, or set it everywhere it is written.
# `just version` prints shim/index.mjs's EXPECTED_VERSION; `just version 1.2.3`
# (a leading v is optional) writes v1.2.3 to EXPECTED_VERSION,
# cmd/agentic-review/main.go's version, and docs/setup.md's ref: pins.
# Checks every pattern before writing and every value after; never commits,
# tags, or pushes.
version $new="":
    #!/usr/bin/env bash
    set -euo pipefail
    fail() { echo "version: $*" >&2; exit 1; }
    shim=shim/index.mjs
    main=cmd/agentic-review/main.go
    docs=docs/setup.md
    for f in "$shim" "$main" "$docs"; do
        [ -f "$f" ] || fail "$f not found (run from the repo root)"
    done
    current=$(sed -n 's/^export const EXPECTED_VERSION = "\(.*\)";$/\1/p' "$shim")
    [ -n "$current" ] || fail "no EXPECTED_VERSION line in $shim"
    if [ -z "$new" ]; then
        echo "$current"
        exit 0
    fi
    v=${new#v}
    [[ "$v" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "'$new' is not X.Y.Z (a leading v is optional)"
    want="v$v"
    shim_re='^export const EXPECTED_VERSION = "[^"]*";$'
    main_re='^var version = "[^"]*"$'
    pin_re='^ +ref: v[0-9]+\.[0-9]+\.[0-9]+([[:space:]]|$)'
    # Check every pattern before writing anything, so a miss leaves no file half-bumped.
    grep -qE "$shim_re" "$shim" || fail "no EXPECTED_VERSION line in $shim"
    grep -qE "$main_re" "$main" || fail "no 'var version = \"...\"' line in $main"
    pins=$(grep -cE "$pin_re" "$docs" || true)
    [ "$pins" -gt 0 ] || fail "no 'ref: vX.Y.Z' pins in $docs"
    # BSD and GNU sed disagree on -i; rewrite through a temp file instead.
    rewrite() {
        local tmp
        tmp=$(mktemp)
        sed -E "$2" "$1" > "$tmp"
        cat "$tmp" > "$1"
        rm -f "$tmp"
    }
    rewrite "$shim" "s/$shim_re/export const EXPECTED_VERSION = \"$want\";/"
    rewrite "$main" "s/$main_re/var version = \"$want\"/"
    rewrite "$docs" "s/^( +ref: )v[0-9]+\.[0-9]+\.[0-9]+([[:space:]]|$)/\1$want\2/"
    grep -qxF "export const EXPECTED_VERSION = \"$want\";" "$shim" || fail "EXPECTED_VERSION in $shim is not $want after rewrite"
    grep -qxF "var version = \"$want\"" "$main" || fail "version in $main is not $want after rewrite"
    got=$(grep -cE "^ +ref: ${want//./\\.}([[:space:]]|$)" "$docs" || true)
    [ "$got" -eq "$pins" ] || fail "$got of $pins ref: pins in $docs read $want after rewrite"
    echo "version: $current -> $want ($shim, $main, $docs)"

```

- [x] **Step 2: Point `just release`'s closing hint at the recipe**

Replace `justfile` line 105 (the last `echo` in `release`) with:

```just
    echo "release: then copy checksums.txt into CHECKSUMS in shim/index.mjs and run 'just version <next>' (docs/setup.md section 5)"
```

- [x] **Step 3: Document it**

In `AGENTS.md`'s command block, insert immediately above the `just release` line (124):

```text
just version        # print the app version (shim EXPECTED_VERSION); `just version X.Y.Z`
                     # (leading v optional) writes vX.Y.Z to the shim, main.go, docs/setup.md
```

Replace `docs/setup.md` lines 298-299 (step 2 of section 5) with:

```markdown
2. Copy those checksums into `CHECKSUMS` in `shim/index.mjs`, keyed
   `<os>-<arch>`, and run `just version <next>` to move `EXPECTED_VERSION`,
   the binary's version, and this guide's `ref:` pins to the *next* tag.
```

- [x] **Step 4: Verify behavior (throwaway checks, nothing kept)**

Run each and compare:

```bash
files="shim/index.mjs cmd/agentic-review/main.go docs/setup.md"
before=$(shasum $files)
just version                       # prints v0.1.0
just version 1.2.3                 # prints: version: v0.1.0 -> v1.2.3 (...)
just version                       # prints v1.2.3
grep -c 'v1.2.3' $files            # 1, 1, 2
go test ./cmd/agentic-review       # PASS (literals still agree)
just version v0.1.0                # leading v accepted; restores
[ "$before" = "$(shasum $files)" ] && echo restored          # restored
just version 1.2;        echo "exit $?"   # version: '1.2' is not X.Y.Z ...; exit 1
just version 1.2.3-rc.1; echo "exit $?"   # rejected; exit 1
just version vv1.2.3;    echo "exit $?"   # rejected (one v stripped); exit 1
[ "$before" = "$(shasum $files)" ] && echo unchanged         # unchanged
```

Pattern-miss leaves nothing half-written:

```bash
tmp=$(mktemp -d)
mkdir -p "$tmp/shim" "$tmp/cmd/agentic-review" "$tmp/docs"
cp justfile "$tmp/"; cp shim/index.mjs "$tmp/shim/"; cp docs/setup.md "$tmp/docs/"
printf 'package main\n' > "$tmp/cmd/agentic-review/main.go"
just --justfile "$tmp/justfile" --working-directory "$tmp" version 9.9.9; echo "exit $?"   # no 'var version' line; exit 1
grep -c v9.9.9 "$tmp/shim/index.mjs" "$tmp/docs/setup.md"                                  # 0, 0
rm -rf "$tmp"
```

### Task 4: GoReleaser config, tool pin, and `just snapshot`

**Files:**

- Create: `.goreleaser.yaml`
- Modify: `.mise.toml`
- Modify: `justfile` (new `snapshot` recipe immediately after `smoke`)
- Modify: `AGENTS.md:106`, `AGENTS.md` command block, `AGENTS.md` Important Files list

- [x] **Step 1: Pin GoReleaser**

Add to `.mise.toml` under `[tools]`, after `golangci-lint = "2"`:

```toml
goreleaser = "2.18.3"
```

Run: `mise install goreleaser && goreleaser --version`
Expected: reports `2.18.3`

- [x] **Step 2: Create `.goreleaser.yaml`**

```yaml
# Release build for agentic-review (docs/decisions/
# 2026-10-12-version-source-of-truth-and-goreleaser-builds.md).
# release.yml runs `goreleaser release --clean` on v* tags; `just snapshot`
# runs the same builds without publishing, locally and in ci.yml.
version: 2

project_name: agentic-review

builds:
  - id: agentic-review
    main: ./cmd/agentic-review
    binary: agentic-review
    env:
      - CGO_ENABLED=0
    flags:
      - -trimpath
    # The tag the shim downloads by. `just release` only ever tags
    # EXPECTED_VERSION, which main.go's version literal already equals.
    ldflags:
      - -X main.version={{ .Tag }}
    targets:
      - linux_amd64_v1
      - linux_arm64
      - darwin_arm64

archives:
  - id: binaries
    formats:
      - binary
    # Asset names the shim's platformKey() and spec §13.1's bake snippet use.
    name_template: "{{ .ProjectName }}-{{ .Os }}-{{ .Arch }}"

checksum:
  name_template: checksums.txt
  algorithm: sha256

changelog:
  use: github-native
```

Run: `goreleaser check`
Expected: `1 configuration file(s) validated`, exit 0, no deprecation warnings

- [x] **Step 3: Add the snapshot recipe**

Insert into `justfile` immediately after the `smoke` recipe (after its `@echo "smoke: OK"` line and blank line):

```just
# Cross-compile every release platform from .goreleaser.yaml into dist/
# without publishing; ci.yml's build job runs this. Needs goreleaser from
# `just deps`.
snapshot:
    goreleaser build --snapshot --clean

```

- [x] **Step 4: Verify builds and release asset names (throwaway checks)**

Run: `just snapshot`
Expected: exit 0; three binaries under `dist/` (`find dist -name agentic-review -type f` lists three)

Run: `goreleaser release --snapshot --clean && cat dist/checksums.txt && ./dist/agentic-review_darwin_arm64_v8.0/agentic-review version` (snapshot mode skips publishing; if it fails in the changelog step for lack of a GitHub token, rerun prefixed with `GITHUB_TOKEN="$(gh auth token)"`)
Expected: `checksums.txt` lists exactly `agentic-review-darwin-arm64`, `agentic-review-linux-amd64`, `agentic-review-linux-arm64` (with `formats: [binary]` the renamed assets exist as artifact names in `dist/artifacts.json`, pointing at the per-target `dist/agentic-review_<target>/agentic-review` builds); the darwin binary prints `v0.1.0`

Run: `rm -rf dist`

- [x] **Step 5: Document it**

Replace `AGENTS.md` line 106's parenthetical `(go 1.26.6, node 24, just, golangci-lint 2)` with `(go 1.26.6, node 24, just, golangci-lint 2, goreleaser 2.18.3)`, keeping the rest of the sentence.

In `AGENTS.md`'s command block, insert immediately above the `just version` lines added in Task 3:

```text
just snapshot       # goreleaser build --snapshot --clean: cross-compile every release
                     # platform from .goreleaser.yaml into dist/, no publishing
```

In `AGENTS.md`'s Important Files list, insert immediately after the `action.yml` entry:

```markdown
- `.goreleaser.yaml` — release build: platforms, `-trimpath`, `CGO_ENABLED=0`,
  `-X main.version={{ .Tag }}`, asset names (`agentic-review-<os>-<arch>`), `checksums.txt`.
```

### Task 5: Release and CI workflows on GoReleaser

**Files:**

- Modify: `.github/workflows/release.yml` (whole file)
- Create: `.github/actionlint.yaml`
- Modify: `.github/workflows/ci.yml:24-45` (`build` job)
- Modify: `AGENTS.md:129-132`
- Modify: `docs/setup.md:294-297`

- [ ] **Step 1: Release via GoReleaser**

Replace `.github/workflows/release.yml` with:

```yaml
name: Release

# Spec §13.1/item 48: on a v* tag, GoReleaser (.goreleaser.yaml)
# cross-compiles every released platform, checksums the artifacts, and
# publishes a GitHub Release. shim/index.mjs's CHECKSUMS map must then be
# updated with this run's checksums.txt entries before the
# download-fallback ladder step works (docs/setup.md section 5).

on:
  push:
    tags: ["v*"]

permissions:
  contents: write

jobs:
  release:
    runs-on: arc-ubuntu-24.04-arm64
    steps:
      - uses: actions/checkout@v7.0.1
        with:
          fetch-depth: 0

      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: go.mod

      - uses: goreleaser/goreleaser-action@v7.3.0
        with:
          distribution: goreleaser
          version: v2.18.3
          args: release --clean
        env:
          GITHUB_TOKEN: ${{ github.token }}
```

- [ ] **Step 2: CI builds via the snapshot recipe**

Replace the `build` job in `.github/workflows/ci.yml` (lines 24-45) with:

```yaml
  build:
    needs: check
    # Same fork routing as check.
    runs-on: ${{ github.event_name == 'pull_request' && github.event.pull_request.head.repo.full_name != github.repository && 'ubuntu-24.04-arm' || 'arc-ubuntu-24.04-arm64' }}
    steps:
      - uses: actions/checkout@v7.0.1
      - uses: actions/setup-go@v7.0.0
        with:
          go-version-file: go.mod
      - uses: jdx/mise-action@v4.2.5
      # Builds every release platform from .goreleaser.yaml without
      # publishing, so a broken release config fails here, not at tag time.
      - run: just snapshot
```

- [ ] **Step 3: Document it**

Replace `AGENTS.md` lines 129-132 (the paragraph starting "CI (`.github/workflows/ci.yml`)") with:

```markdown
CI (`.github/workflows/ci.yml`) runs `just check`, then `just snapshot` (GoReleaser
cross-compiles linux/amd64, linux/arm64, darwin/arm64 from `.goreleaser.yaml`).
`dogfood.yml` self-reviews every PR with the in-flight binary via `uses: ./`.
`release.yml` runs GoReleaser on `v*` tags to publish the binaries + `checksums.txt`;
bump the version with `just version`, cut tags with `just release`.
```

Replace `docs/setup.md` lines 294-297 (step 1 of section 5) with:

```markdown
1. Run `just release` from a clean, up-to-date `main`. It tags the
   shim's `EXPECTED_VERSION`, runs `just check` first, and pushes the tag.
   `release.yml` runs GoReleaser (`.goreleaser.yaml`), which cross-compiles
   linux/amd64, linux/arm64, and darwin/arm64 and publishes them with
   `checksums.txt`.
```

- [ ] **Step 4: Declare the ARC runner label for actionlint**

actionlint rejects the self-hosted label `arc-ubuntu-24.04-arm64` as unknown (already the case on BASE in `evals.yml` and `release.yml`). Create `.github/actionlint.yaml`:

```yaml
# Self-hosted runner labels actionlint cannot know about: the ARC scale set
# the workflows run on (fork PRs route to GitHub-hosted runners instead).
self-hosted-runner:
  labels:
    - arc-ubuntu-24.04-arm64
```

- [ ] **Step 5: Verify**

Run: `actionlint`
Expected: no output, exit 0

Run: `grep -rn 'main.version=' --include='*.yml' --include='*.yaml' --include='*.md' --include='justfile' . | grep -v -e '^./docs/decisions/' -e '^./PLAN.md:'`
Expected: only `.goreleaser.yaml` and `AGENTS.md`'s `.goreleaser.yaml` entry

Run: `just check && just smoke && just validate && just snapshot && rm -rf dist`
Expected: all pass

## Status

Task 4 built and checked; Task 3's `version` recipe and the new `snapshot` recipe now put their one-line summary directly above the recipe. Task 5 gained a step adding `.github/actionlint.yaml` (human decision): repo-wide `actionlint` flagged the custom ARC runner label in unmodified `evals.yml` and `release.yml`. GNU sed/grep not exercised locally. Note: `go test -ldflags '-X main.version=…'` does not reach the package under test; release `go build` of the main package is unaffected.
