# `just release` Recipe Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use subagent-driven-development (recommended) or executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a `release` recipe to `justfile` that preflights the repo, runs `just check`, then creates and pushes the tag read from the shim's `EXPECTED_VERSION`, which triggers `.github/workflows/release.yml`.

**Decision:** none — Direct path

**Base:** 901425f5f1241b990428a223c2e965b826de0351

**Architecture:** One `#!/usr/bin/env bash` recipe in `justfile`, matching the existing `live-smoke` shebang-recipe style. The tag is never an argument: it is read from `shim/index.mjs` with the same `sed` expression `docs/setup.md` §4 already uses, so the pushed tag and the shim's version check cannot drift. `docs/setup.md` §5 gets one pointer to the recipe.

**Tech Stack:** just, bash, git. No Go code changes.

---

## Behavior contract

`just release`, with no arguments, MUST:

1. Read `tag` from `shim/index.mjs` (`export const EXPECTED_VERSION = "vX.Y.Z";`). Abort if empty or if it does not match `^v[0-9]+\.[0-9]+\.[0-9]+$`.
2. Abort unless the current branch is `main`.
3. Abort unless `git status --porcelain` is empty.
4. `git fetch --quiet origin main`, then abort unless `HEAD` equals `origin/main`.
5. Abort if `refs/tags/<tag>` exists locally, or if `git ls-remote --tags origin refs/tags/<tag>` is non-empty.
6. Run `just check`.
7. `git tag -a <tag> -m <tag>` then `git push origin <tag>`.
8. Print the tag, the Actions URL `https://github.com/phatblat/agentic-review/actions/workflows/release.yml`, and the follow-up from `docs/setup.md` §5 (copy `checksums.txt` into `CHECKSUMS`, bump `EXPECTED_VERSION` to the next tag).

Every abort prints `release: <reason>` to stderr and exits non-zero **before** any tag is created or pushed. Nothing else changes: no edits to `shim/index.mjs`, `release.yml`, or the `smoke` recipe.

### Task 1: Add the `release` recipe

**Files:**
- Modify: `justfile` (insert between the `live-smoke` recipe, ending at line 76, and `clean` at line 78)

- [x] **Step 1: Insert the recipe**

Insert this block after `live-smoke` (and its trailing blank line) and before `clean`, keeping one blank line between recipes:

```just
# Tag the shim's EXPECTED_VERSION on an up-to-date, clean main and push
# the tag, which triggers .github/workflows/release.yml. Takes no
# argument on purpose: the tag always equals EXPECTED_VERSION, the
# version every released binary is stamped with and the shim checks for.
# Aborts before touching any ref if a preflight fails. See docs/setup.md
# section 5 for what to do after the release publishes.
release:
    #!/usr/bin/env bash
    set -euo pipefail
    fail() { echo "release: $*" >&2; exit 1; }
    tag=$(sed -n 's/^export const EXPECTED_VERSION = "\(.*\)";$/\1/p' shim/index.mjs)
    [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "EXPECTED_VERSION in shim/index.mjs is '$tag', want vX.Y.Z"
    branch=$(git branch --show-current)
    [ "$branch" = "main" ] || fail "must run on main (on '$branch')"
    [ -z "$(git status --porcelain)" ] || fail "working tree is not clean"
    git fetch --quiet origin main
    [ "$(git rev-parse HEAD)" = "$(git rev-parse origin/main)" ] || fail "HEAD differs from origin/main; push or pull first"
    git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null && fail "tag $tag already exists locally"
    [ -z "$(git ls-remote --tags origin "refs/tags/$tag")" ] || fail "tag $tag already exists on origin"
    just check
    git tag -a "$tag" -m "$tag"
    git push origin "$tag"
    echo "release: pushed $tag"
    echo "release: watch https://github.com/phatblat/agentic-review/actions/workflows/release.yml"
    echo "release: then copy checksums.txt into CHECKSUMS in shim/index.mjs and bump EXPECTED_VERSION to the next tag (docs/setup.md section 5)"
```

- [x] **Step 2: Confirm just parses it and lists it**

Run: `just --list`
Expected: exit 0, output includes a `release` line whose description is the last comment line above the recipe (`just --list` shows the last line).

Run: `just --show release`
Expected: prints the recipe body exactly as inserted.

- [x] **Step 3: Throwaway verification in a scratch repo (never the real repo)**

The real repo is on a feature branch, so running `just release` there aborts at the branch check. Do NOT run it with a real `origin`. Build a scratch setup under `$(mktemp -d)`; commits and pushes inside the scratch repos are allowed, and only there:

```bash
S=$(mktemp -d)
git init --bare "$S/origin.git" -b main
git clone "$PWD" "$S/work"            # read-only on the real repo
cp justfile "$S/work/justfile"        # carry the new recipe
cd "$S/work"
git remote set-url origin "$S/origin.git"
git switch -C main
git add justfile && git -c user.name=t -c user.email=t@t commit -qm "scratch" 
git push -q origin main
git fetch -q origin
```

Then exercise each case from `$S/work` (use the bash tool's `cwd`, never `cd` inside a command). Each abort case must print the exact `release: …` message, exit non-zero, and leave `git -C $S/origin.git tag -l` empty:

| Case | How | Expected message |
|---|---|---|
| wrong branch | `git switch -q -c other` then `just release`; switch back to `main` after | `release: must run on main (on 'other')` |
| dirty tree | `echo x >> README.md` then `just release`; `git checkout README.md` after | `release: working tree is not clean` |
| HEAD ahead of origin | `git commit --allow-empty -qm ahead` then `just release`; `git reset -q --hard origin/main` after | `release: HEAD differs from origin/main; push or pull first` |
| bad version | temporarily set `EXPECTED_VERSION = "dev"` in `shim/index.mjs`, commit it and `git push -q origin main`, run `just release`; then restore, commit, push | `release: EXPECTED_VERSION in shim/index.mjs is 'dev', want vX.Y.Z` |
| tag exists locally | `git tag v0.1.0` then `just release`; `git tag -d v0.1.0` after | `release: tag v0.1.0 already exists locally` |
| tag exists on origin | `git push -q origin v0.1.0` from a tag created and deleted locally, then `just release`; then delete it from the bare repo with `git -C $S/origin.git tag -d v0.1.0` | `release: tag v0.1.0 already exists on origin` |
| success | clean `main`, in sync, no tags, then `just release` | exits 0; `git -C $S/origin.git tag -l` prints `v0.1.0`; `git -C $S/origin.git cat-file -t v0.1.0` prints `tag` (annotated); stdout ends with the three `release:` lines |

Expected: every row behaves as stated. If a row fails, fix the recipe in the real repo's `justfile`, re-copy it to `$S/work`, and rerun the failing row.

- [x] **Step 4: Remove the scratch directory**

Run: `rm -rf "$S"`
Then confirm in the real repo: `git status --porcelain` shows only ` M justfile` (and `PLAN.md`), and `git tag -l` is empty.

### Task 2: Point `docs/setup.md` §5 at the recipe

**Files:**
- Modify: `docs/setup.md` (§5 "Releasing (optional)", step 1, currently lines 294-295)

- [x] **Step 1: Edit step 1 of the list**

Current text:

```
1. Tag `vX.Y.Z`. `release.yml` cross-compiles linux/amd64, linux/arm64, and
   darwin/arm64, then publishes `checksums.txt`.
```

Replace with:

```
1. Run `just release` from a clean, up-to-date `main`. It tags the
   shim's `EXPECTED_VERSION`, runs `just check` first, and pushes the tag.
   `release.yml` cross-compiles linux/amd64, linux/arm64, and
   darwin/arm64, then publishes `checksums.txt`.
```

Leave steps 2 and 3 untouched.

- [x] **Step 2: Confirm the rendered section**

Run: `git diff -- docs/setup.md`
Expected: only the step-1 lines changed.

## Status

Review fixes applied and checked (`just check`, `just --list`, scratch-repo matrix incl. fetch/ls-remote/push/check failures); awaiting second review round.
