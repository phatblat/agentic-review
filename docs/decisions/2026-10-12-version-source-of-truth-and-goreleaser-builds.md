# Version source of truth and GoReleaser builds

## Issue

`just release` refuses to run once the version it would tag already exists
(`release: tag v0.1.0 already exists locally`), and nothing in the repository
moves the version forward: a bump means hand-editing `EXPECTED_VERSION` in
`shim/index.mjs`, the `v0.1.0` literal in `just smoke`, and both `ref:` pins
in `docs/setup.md`.

The version reaches the binary only through
`-ldflags "-X main.version=…"`, so every non-release build re-derives
`EXPECTED_VERSION` with `sed` and passes it back in: `dogfood.yml`, both jobs
of the consumer workflow in `docs/setup.md` §4, and (hardcoded) `just smoke`.
The release build's platform list, `-trimpath`, `CGO_ENABLED=0`, checksumming,
and publishing are inline shell in `release.yml`, with a second copy of the
platform list in `ci.yml`'s build matrix.

## Status

This is a proposal that is **accepted**.

## Assumptions and Constraints

- The shim trusts a binary only when `<bin> version` prints exactly
  `EXPECTED_VERSION` (`checkVersion` in `shim/index.mjs`). Every build handed
  to the shim (dogfood, the `docs/setup.md` §4 consumer workflow,
  `just smoke`, a baked runner image) must report that exact `v`-prefixed
  string.
- The shim's download rung fetches
  `releases/download/<EXPECTED_VERSION>/agentic-review-<os>-<arch>` and
  verifies it against `CHECKSUMS`; `docs/spec.md` §13.1's bake snippet uses
  the same asset names. Release asset names must not change.
- Released platforms are `linux/amd64`, `linux/arm64`, and `darwin/arm64`,
  built with `CGO_ENABLED=0` and `-trimpath`.
- `just release` tags exactly `EXPECTED_VERSION` and requires `vX.Y.Z`; no
  pre-release or build metadata.
- The shim stays zero-dependency Node; no `package.json`.
- Go 1.24+ stamps a VCS-derived main-module version readable through
  `runtime/debug.ReadBuildInfo`. On an untagged commit it is a pseudo-version
  (`v0.1.1-0.20261013011939-868fbc65f9bd` observed at `868fbc6`), which can
  never equal `EXPECTED_VERSION`.

## Argument

`-ldflags -X` remains the right way to stamp a release binary with its tag;
GoReleaser's default build does the same. The defect is that ldflags is the
*only* way the version reaches the binary. Holding the version as a source
literal in `main.go`, pinned to the shim by a test, makes a plain `go build`
correct, deletes every `sed` + `-ldflags` copy, and leaves one recipe that
edits every literal. **Chosen.**

Go has no per-repository build configuration file. GoReleaser's
`.goreleaser.yaml` is the ecosystem's standard home for the platform matrix,
build flags, ldflags, checksums, and publishing, and it replaces the inline
shell in `release.yml`. Running its snapshot build in CI makes a broken release
configuration fail on the pull request rather than at tag time. **Chosen.**

A VCS-derived version, a single embedded `VERSION` file, and `GOFLAGS` in
`.mise.toml` are **Rejected**; see Positions.

## Architectural Decision

1. **Version literals.** The app version is written `vX.Y.Z` in exactly these
   places: `EXPECTED_VERSION` in `shim/index.mjs`, the `var version`
   initializer in `cmd/agentic-review/main.go` (replacing `"dev"`), and the
   two `ref:` pins of `phatblat/agentic-review` in `docs/setup.md` §4.
   Comments that name a specific version are made version-agnostic.
   Classifier fixtures (`internal/classes/manifest/manifest_test.go`,
   `fixtures/classes/cargo/**`) carry a test crate's version, not the app's,
   and are untouched.
2. **Drift test.** A test in `cmd/agentic-review` reads `shim/index.mjs` and
   fails when its `EXPECTED_VERSION` differs from `version`, so `just check`,
   and therefore `just release`, catches drift.
3. **Plain builds.** `dogfood.yml`, both jobs of the `docs/setup.md` §4
   consumer workflow, and `just smoke` build with plain `go build`: no `sed`,
   no `-ldflags`.
4. **`just version`.**
   - With no argument, it prints `EXPECTED_VERSION` read from
     `shim/index.mjs`.
   - With one argument, it strips a single leading `v`, rejects anything not
     matching `^[0-9]+\.[0-9]+\.[0-9]+$`, writes `v<X.Y.Z>` to every place in
     clause 1, then verifies each file holds the new value. A pattern that
     matches nothing fails the recipe; it never silently does nothing.
   - It runs with both BSD (macOS) and GNU tools, and never commits, tags, or
     pushes.
5. **GoReleaser configuration.** `.goreleaser.yaml` (`version: 2`) defines the
   release build. Its contract:

   ```yaml
   project_name: agentic-review
   builds:
     - main: ./cmd/agentic-review
       binary: agentic-review
       env: [CGO_ENABLED=0]
       flags: [-trimpath]
       ldflags: ["-X main.version={{ .Tag }}"]
       targets: [linux_amd64, linux_arm64, darwin_arm64]
   archives:
     - formats: [binary]
       name_template: "{{ .ProjectName }}-{{ .Os }}-{{ .Arch }}"
   checksum:
     name_template: checksums.txt
     algorithm: sha256
   changelog:
     use: github-native
   ```

   Asset names (`agentic-review-<os>-<arch>`, `checksums.txt`) are unchanged.
   Release binaries report the tag they were published under.
6. **Release workflow.** `release.yml` runs one job: checkout with full
   history, `actions/setup-go` from `go.mod`, and
   `goreleaser/goreleaser-action@v7.3.0` running `release --clean` with
   GoReleaser pinned to `v2.18.3`. The `v*` tag trigger, `contents: write`
   permission, and runner label are unchanged.
7. **Tool pin.** `.mise.toml` pins `goreleaser = "2.18.3"`, the version the
   release workflow uses.
8. **Snapshot build.** `just snapshot` runs
   `goreleaser build --snapshot --clean`, writing to the gitignored `dist/`.
   `ci.yml`'s build job replaces its three-platform `go build ./...` matrix
   with one job that installs the mise toolchain and runs `just snapshot`,
   keeping `needs: check` and the fork-routing `runs-on` expression.
9. **Docs.** `AGENTS.md` (commands, CI and release description),
   `docs/setup.md` §4–§5, and the closing hint of `just release` describe
   `just version` and GoReleaser. `just release` is otherwise unchanged.

## Consequences

- Unstamped builds (`just build`, `go run`) report the current
  `EXPECTED_VERSION` instead of `dev`; a version string no longer
  distinguishes a development build from a release. Every build stamped by the
  removed `sed` + `-ldflags` steps already behaved this way.
- Between a `just version <next>` bump and the release that tags it,
  `docs/setup.md` on `main` pins a tag that does not exist yet.
- `just snapshot` needs GoReleaser from `mise install`; `just build`,
  `just test`, and `just check` do not.
- CI cross-compiles only what `./cmd/agentic-review` imports for each release
  platform; `just check` still compiles every package on Linux.

## Positions

- **VCS-derived version (`runtime/debug.ReadBuildInfo`).** Rejected: dogfood,
  the consumer workflow, and smoke build untagged commits whose pseudo-versions
  never equal `EXPECTED_VERSION`, so the shim would reject them unless
  `-ldflags` stayed or the shim's exact-match check were relaxed.
- **Single embedded `VERSION` file read by the binary and the shim.**
  Rejected: one write target and no ldflags at all, but the shim would read a
  file at runtime instead of a constant, and `just release` and the docs
  extraction would move with it; a larger change to the trust path than this
  problem needs.
- **Recipe edits literals; ldflags stays the only stamping path.** Rejected:
  keeps every `sed` + `-ldflags` copy for no benefit.
- **`GOFLAGS` in `.mise.toml` `[env]`.** Rejected: reaches only mise users;
  the release, dogfood, and consumer workflows use `actions/setup-go` without
  mise.
- **Full semver input (pre-release, build metadata).** Rejected: `just
  release`, the shim, and release tags all assume `vX.Y.Z`; widening them is a
  separate decision.

## References

- `docs/setup.md` §4–§5 — consumer workflow and release procedure this
  decision changes.
- `docs/spec.md` §13.1 — shim resolution ladder, version check, and release
  asset names.
- [GoReleaser archives](https://goreleaser.com/customization/package/archives/)
  — the `binary` format and its `name_template`.
- [GoReleaser GitHub Actions](https://goreleaser.com/customization/ci/actions/)
  — `goreleaser-action` and the `fetch-depth: 0` requirement.
- [Go 1.24 release notes](https://go.dev/doc/go1.24#go-command) — VCS-derived
  main-module version.
