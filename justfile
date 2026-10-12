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
    go build -ldflags "-X main.version=v0.1.0" -o /tmp/agentic-review-shim-smoke ./cmd/agentic-review
    AGENTIC_REVIEW_BIN=/tmp/agentic-review-shim-smoke GITHUB_EVENT_PATH="$(pwd)/fixtures/replay/security-token-expiry/event.json" GITHUB_EVENT_NAME=pull_request AGENTIC_REVIEW_DRY_RUN=1 node shim/index.mjs >/dev/null
    out=$(AGENTIC_REVIEW_BIN=/bin/true GITHUB_EVENT_PATH="$(pwd)/fixtures/replay/security-token-expiry/event.json" GITHUB_EVENT_NAME=pull_request AGENTIC_REVIEW_DRY_RUN=1 node shim/index.mjs 2>&1 || true); echo "$out" | grep -q "falling through"
    rm -f /tmp/agentic-review-shim-smoke
    @echo "smoke: OK"

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
    git tag -a "$tag" -m "$tag" "$head"
    git push origin "refs/tags/$tag" || { git tag -d "$tag" >/dev/null; fail "push of $tag failed; local tag removed, fix and rerun"; }
    echo "release: pushed $tag"
    echo "release: watch https://github.com/phatblat/agentic-review/actions/workflows/release.yml"
    echo "release: then copy checksums.txt into CHECKSUMS in shim/index.mjs and bump EXPECTED_VERSION to the next tag (docs/setup.md section 5)"

clean:
    rm -rf agentic-review dist .agentic-review
