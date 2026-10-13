set shell := ["bash", "-euo", "pipefail", "-c"]

@_default:
    just --list

# Install pinned toolchain (go, node, just, golangci-lint via mise) and
# download Go module dependencies. Run this once per clone/CI runner
# before build/test/lint.
deps:
    mise install
    go mod download

# Build the agentic-review binary.
build:
    go build -o agentic-review ./cmd/agentic-review

# Run the full test suite.
test:
    go test ./...

# Rewrite golden fixtures for every package that has them. Scoped
# (rather than `go test ./... -update`): go test's flag.Parse() rejects
# an unrecognized flag per test binary, and only these three packages
# import internal/goldentest, which registers -update.
test-update:
    go test ./internal/classes ./internal/roster ./internal/runner -update

# `golangci-lint fmt` (gofmt + goimports from .golangci.yml) runs first
# because it still formats a tree that does not type-check, whereas
# `golangci-lint run` bails on typecheck errors before fixing anything.
# `run --fix` then applies the autofixable linters. --issues-exit-code=0
# keeps residual non-autofixable findings (unused, most staticcheck)
# informational; tool/config failures still exit 3. `go vet` has no
# auto-fix, so nothing mirrors it here.
# Auto-fix everything `just lint` reports that can be fixed.
format:
    golangci-lint fmt
    golangci-lint run --fix --issues-exit-code=0

# gofmt, go vet, and golangci-lint; no formatting or auto-fixing.
lint:
    @test -z "$(gofmt -l .)" || (gofmt -l . && exit 1)
    go vet ./...
    golangci-lint run

# lint + test.
check: lint test

# Load-check every builtin and repo-local persona plus config.yaml.
validate:
    go run ./cmd/agentic-review validate

# CLI smoke (Verification item 8, no models) + shim smoke (item 9, no
# network): both must be reproducible without a live inference endpoint.
smoke:
    go run ./cmd/agentic-review plan --triage fixtures/cli-smoke/high-risk-auth/assessment.json --config fixtures/cli-smoke/high-risk-auth >/dev/null
    go build -o /tmp/agentic-review-shim-smoke ./cmd/agentic-review
    AGENTIC_REVIEW_BIN=/tmp/agentic-review-shim-smoke GITHUB_EVENT_PATH="$(pwd)/fixtures/replay/security-token-expiry/event.json" GITHUB_EVENT_NAME=pull_request AGENTIC_REVIEW_DRY_RUN=1 node shim/index.mjs >/dev/null
    out=$(AGENTIC_REVIEW_BIN=/bin/true GITHUB_EVENT_PATH="$(pwd)/fixtures/replay/security-token-expiry/event.json" GITHUB_EVENT_NAME=pull_request AGENTIC_REVIEW_DRY_RUN=1 node shim/index.mjs 2>&1 || true); echo "$out" | grep -q "falling through"
    rm -f /tmp/agentic-review-shim-smoke
    @echo "smoke: OK"

# Cross-compile every release platform from .goreleaser.yaml into dist/
# without publishing; ci.yml's build job runs this. Needs goreleaser from
# `just deps`.
# Cross-compile every release platform into dist/ without publishing.
snapshot:
    goreleaser build --snapshot --clean

# Live smoke (Verification item 10) — only runs when a real inference
# endpoint and model are configured; skips with a notice otherwise and
# never fails the build for missing deployment settings.
live-smoke:
    #!/usr/bin/env bash
    set -euo pipefail
    if [ -z "${AGENTIC_REVIEW_ENDPOINT:-}" ] || [ -z "${AGENTIC_REVIEW_MODEL:-}" ]; then
        echo "live-smoke: AGENTIC_REVIEW_ENDPOINT or AGENTIC_REVIEW_MODEL is unset, skipping"
        exit 0
    fi
    rm -rf /tmp/agentic-review-live-smoke-rec
    go run ./cmd/agentic-review triage --diff fixtures/diffs/auth-token.diff --record /tmp/agentic-review-live-smoke-rec
    test -n "$(find /tmp/agentic-review-live-smoke-rec/triage -type f 2>/dev/null)"
    echo "live-smoke: OK"

# `just version` prints shim/index.mjs's EXPECTED_VERSION; `just version 1.2.3`
# (a leading v is optional) writes v1.2.3 to EXPECTED_VERSION,
# cmd/agentic-review/main.go's version, and docs/setup.md's ref: pins.
# Checks every pattern before writing and every value after; never commits,
# tags, or pushes.
# Print the app version, or set it everywhere it is written.
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

# Tag EXPECTED_VERSION on clean, up-to-date main and push it to trigger release.yml.
release:
    #!/usr/bin/env bash
    set -euo pipefail
    # Takes no argument on purpose: the tag always equals EXPECTED_VERSION,
    # the version every released binary is stamped with and the shim checks
    # for. Aborts before creating or pushing any tag if a preflight fails.
    # See docs/setup.md section 5 for what to do after the release publishes.
    fail() { echo "release: $*" >&2; exit 1; }
    [ -f shim/index.mjs ] || fail "shim/index.mjs not found (run from the repo root)"
    tag=$(sed -n 's/^export const EXPECTED_VERSION = "\(.*\)";$/\1/p' shim/index.mjs)
    [[ "$tag" =~ ^v[0-9]+\.[0-9]+\.[0-9]+$ ]] || fail "EXPECTED_VERSION in shim/index.mjs is '$tag', want vX.Y.Z"
    branch=$(git branch --show-current) || fail "not a git repository"
    [ "$branch" = "main" ] || fail "must run on main (on '${branch:-detached HEAD}')"
    st=$(git status --porcelain) || fail "git status failed"
    [ -z "$st" ] || fail "working tree is not clean"
    git fetch --quiet origin main || fail "could not fetch origin main"
    head=$(git rev-parse HEAD)
    [ "$head" = "$(git rev-parse --verify refs/remotes/origin/main)" ] || fail "HEAD differs from origin/main; push or pull first"
    if git rev-parse --quiet --verify "refs/tags/$tag" >/dev/null; then fail "tag $tag already exists locally"; fi
    remote_tag=$(git ls-remote --tags origin "refs/tags/$tag") || fail "could not query origin for tag $tag"
    [ -z "$remote_tag" ] || fail "tag $tag already exists on origin"
    just check || fail "just check failed"
    git tag -a "$tag" -m "$tag" "$head" || fail "could not create tag $tag (check tag.gpgSign and git user.name/user.email)"
    git push origin "refs/tags/$tag" || { git tag -d "$tag" >/dev/null; fail "push of $tag failed; local tag removed. Run 'git ls-remote --tags origin' before rerunning: if origin already has $tag, the release workflow may have started"; }
    echo "release: pushed $tag"
    echo "release: watch https://github.com/phatblat/agentic-review/actions/workflows/release.yml"
    echo "release: then copy checksums.txt into CHECKSUMS in shim/index.mjs and run 'just version <next>' (docs/setup.md section 5)"

clean:
    rm -rf agentic-review dist .agentic-review
