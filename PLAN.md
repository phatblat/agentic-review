# Version source of truth and GoReleaser builds: review follow-ups Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Close the in-scope review findings on PR #7: make `just version` insist on exactly the two `phatblat/agentic-review` pins, and make the snapshot/CI/GoReleaser comments accurate.

**Decision:** 2026-10-12-version-source-of-truth-and-goreleaser-builds — Version source of truth and GoReleaser builds (`docs/decisions/2026-10-12-version-source-of-truth-and-goreleaser-builds.md`)

**Base:** 6475f905537d30ae9eb2ad517270b5543085dcc0

**Architecture:** The Decision is already implemented on this branch. These are fixes inside its clauses: clause 1 names exactly two `ref:` pins, so `just version` fails loudly unless it finds exactly two; clauses 8–9 require accurate docs, so comments stop overclaiming what `just snapshot` checks and tie the two GoReleaser version pins together.

**Tech Stack:** `just` (bash shebang recipe), GitHub Actions YAML, mise TOML, actionlint, GoReleaser 2.18.3.

**Rules for every task:**

- Run every command from the repository root.
- Never commit, push, stash, switch branches, or write refs or the index; all commits happen after review.
- Recipes must work with both BSD (macOS) and GNU `sed`/`grep`: never `sed -i`.
- Out of scope (not part of this plan): pinning actions to commit SHAs, `just = "latest"` in `.mise.toml`, the `docs/setup.md` §5 checksum procedure, a release-tag check, a `just version` automated test, and `rewrite()`'s temp-file handling.

---

### Task 1: `just version` requires exactly the two agentic-review pins

**Files:**

- Modify: `justfile:87` (recipe header comment), `justfile:116-117` (pin count check)

- [ ] **Step 1: State the pin contract in the header**

Replace `justfile` line 87:

```just
# cmd/agentic-review/main.go's version, and docs/setup.md's ref: pins.
```

with:

```just
# cmd/agentic-review/main.go's version, and docs/setup.md's two
# phatblat/agentic-review ref: pins.
```

- [ ] **Step 2: Require exactly two pins**

Replace `justfile` lines 116-117:

```just
    pins=$(grep -cE "$pin_re" "$docs" || true)
    [ "$pins" -gt 0 ] || fail "no 'ref: vX.Y.Z' pins in $docs"
```

with:

```just
    # docs/setup.md's review and summons jobs each pin one phatblat/agentic-review
    # checkout; any other count (a SHA pin, another repo's tag) needs a human.
    pins=$(grep -cE "$pin_re" "$docs" || true)
    [ "$pins" -eq 2 ] || fail "expected 2 'ref: vX.Y.Z' pins of phatblat/agentic-review in $docs, found $pins"
```

The post-write check (`[ "$got" -eq "$pins" ]`) stays as is.

- [ ] **Step 3: Verify (throwaway checks, nothing kept)**

Round trip still works and restores byte-for-byte:

```bash
files="shim/index.mjs cmd/agentic-review/main.go docs/setup.md"
before=$(shasum $files)
just version 1.2.3                 # version: v0.1.0 -> v1.2.3 (...)
grep -c 'v1.2.3' $files            # 1, 1, 2
just version 0.1.0                 # restores
[ "$before" = "$(shasum $files)" ] && echo restored          # restored
```

Wrong pin counts fail before writing anything:

```bash
mk() {  # mk DIR: temp copy of the files the recipe reads
    mkdir -p "$1/shim" "$1/cmd/agentic-review" "$1/docs"
    cp justfile "$1/"; cp shim/index.mjs "$1/shim/"
    cp cmd/agentic-review/main.go "$1/cmd/agentic-review/"; cp docs/setup.md "$1/docs/"
}
run() { just --justfile "$1/justfile" --working-directory "$1" version 9.9.9; echo "exit $?"; }

# Third pin (another repo's tag) -> fails
t=$(mktemp -d); mk "$t"
printf '          ref: v3.1.4\n' >> "$t/docs/setup.md"
run "$t"                                          # expected 2 ... found 3; exit 1
grep -c v9.9.9 "$t/shim/index.mjs" "$t/cmd/agentic-review/main.go" "$t/docs/setup.md"   # 0, 0, 0
rm -rf "$t"

# One pin switched to a SHA -> fails
t=$(mktemp -d); mk "$t"
awk 'BEGIN{d=0} /^ +ref: v0\.1\.0/ && !d {sub(/v0\.1\.0/, "0123456789abcdef0123456789abcdef01234567"); d=1} {print}' docs/setup.md > "$t/docs/setup.md"
run "$t"                                          # expected 2 ... found 1; exit 1
grep -c v9.9.9 "$t/shim/index.mjs" "$t/cmd/agentic-review/main.go" "$t/docs/setup.md"   # 0, 0, 0
rm -rf "$t"
```

Run: `just --list`
Expected: `version $new=""` still shows `# Print the app version, or set it everywhere it is written.`

### Task 2: Accurate snapshot, CI, and GoReleaser pin comments

**Files:**

- Modify: `justfile:6` (`deps` comment), `justfile:63-66` (`snapshot` comment)
- Modify: `.github/workflows/ci.yml:35-36`
- Modify: `.github/workflows/release.yml:31`
- Modify: `.mise.toml:6`

- [ ] **Step 1: `deps` lists goreleaser**

Replace `justfile` line 6:

```just
# Install pinned toolchain (go, node, just, golangci-lint via mise) and
```

with:

```just
# Install pinned toolchain (go, node, just, golangci-lint, goreleaser via mise) and
```

- [ ] **Step 2: `snapshot` comment stops repeating its summary**

Replace `justfile` lines 63-66 (the four comment lines above `snapshot:`) with:

```just
# ci.yml's build job runs this; platforms and flags come from .goreleaser.yaml.
# Needs goreleaser from `just deps`.
# Cross-compile every release platform into dist/ without publishing.
```

- [ ] **Step 3: CI comment matches what `goreleaser build` checks**

Replace `.github/workflows/ci.yml` lines 35-36 with:

```yaml
      # Builds every release platform from .goreleaser.yaml's builds (targets,
      # flags, ldflags) without publishing, so a broken build config fails
      # here, not at tag time. Asset names and checksums.txt are produced only
      # by `goreleaser release` on a tag.
```

- [ ] **Step 4: Tie the two GoReleaser version pins together**

Replace `.github/workflows/release.yml` line 31 with:

```yaml
          version: v2.18.3 # keep equal to .mise.toml's goreleaser pin (ci.yml's just snapshot uses it)
```

Replace `.mise.toml` line 6 with:

```toml
goreleaser = "2.18.3" # keep equal to release.yml's goreleaser-action `version:`
```

- [ ] **Step 5: Verify**

Run: `just --list`
Expected: `deps`, `snapshot`, and `version` summaries unchanged (`snapshot  # Cross-compile every release platform into dist/ without publishing.`)

Run: `actionlint`
Expected: no output, exit 0

Run: `mise ls --current goreleaser && goreleaser --version`
Expected: mise still resolves goreleaser 2.18.3; `GitVersion: 2.18.3`

Run: `goreleaser check && just check && just snapshot && rm -rf dist`
Expected: all pass, no `dist/` left

## Status

Not started.
