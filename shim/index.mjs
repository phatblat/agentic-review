#!/usr/bin/env node
// agentic-review's node24 GitHub Actions entrypoint (spec §13.1, plan
// item 45). Zero npm dependencies — node builtins only.
//
// Resolution ladder, in order, every candidate --version-checked before
// being trusted (baking or caching a binary is an optimization, never a
// correctness dependency):
//   1. $AGENTIC_REVIEW_BIN
//   2. /usr/local/bin/agentic-review (baked into the runner image)
//   3. $RUNNER_TOOL_CACHE/agentic-review/<version>/agentic-review
//   4. download from GitHub Releases, verified against a checksum
//      constant baked into this file, then installed into the tool
//      cache for subsequent steps/runs.
//
// The resolved binary is spawned once as `<bin> review`, action inputs
// are mapped to the env vars cmd/agentic-review/review.go reads, stdio
// is forwarded, and this process exits with the child's code.

import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, chmodSync, writeFileSync } from "node:fs";
import { createHash } from "node:crypto";
import path from "node:path";

// EXPECTED_VERSION is the release tag this shim revision was cut for.
// release.yml stamps release binaries with this exact tag via -ldflags;
// dogfood.yml stamps its PR-head build with it too so AGENTIC_REVIEW_BIN
// passes the same version check as every other ladder step.
export const EXPECTED_VERSION = "v0.1.0";

// CHECKSUMS maps "<os>-<arch>" to the sha256 of the EXPECTED_VERSION
// release download for that platform. Populated by release.yml when the
// tag is cut. Empty today: no v0.1.0 artifact exists yet (this repo has
// no git remote and has never been released — see the build plan's
// "Assumptions & contingencies": M11's workflows are inert until then).
// A download attempted before this is populated fails loudly rather
// than skipping verification.
export const CHECKSUMS = {
  // "linux-amd64": "…",
  // "linux-arm64": "…",
  // "darwin-arm64": "…",
};

function log(msg) {
  process.stderr.write(`agentic-review-shim: ${msg}\n`);
}

// checkVersion runs `<bin> version` and reports whether stdout, trimmed,
// equals EXPECTED_VERSION. Any spawn failure (missing binary, not
// executable, or simply not agentic-review at all) also reports false —
// this is also how a placeholder like /bin/true is rejected: it exits 0
// with empty stdout, which never equals EXPECTED_VERSION.
function checkVersion(bin) {
  const r = spawnSync(bin, ["version"], { encoding: "utf8", timeout: 5000 });
  if (r.error || r.status !== 0) return false;
  return (r.stdout || "").trim() === EXPECTED_VERSION;
}

function platformKey() {
  const osName = { linux: "linux", darwin: "darwin" }[process.platform];
  const archName = { x64: "amd64", arm64: "arm64" }[process.arch];
  if (!osName || !archName) {
    throw new Error(`agentic-review-shim: unsupported platform ${process.platform}/${process.arch}`);
  }
  return `${osName}-${archName}`;
}

// resolve walks the ladder above and returns a path to a binary that has
// passed its version check (steps 1-3) or was just downloaded and
// checksum-verified (step 4).
async function resolve() {
  const envBin = process.env.AGENTIC_REVIEW_BIN;
  if (envBin) {
    if (checkVersion(envBin)) return envBin;
    log(`AGENTIC_REVIEW_BIN=${envBin} did not report version ${EXPECTED_VERSION}; falling through`);
  }

  const baked = "/usr/local/bin/agentic-review";
  if (existsSync(baked)) {
    if (checkVersion(baked)) return baked;
    log(`baked binary at ${baked} did not report version ${EXPECTED_VERSION}; falling through to download`);
  }

  const toolCache = process.env.RUNNER_TOOL_CACHE;
  if (toolCache) {
    const cached = path.join(toolCache, "agentic-review", EXPECTED_VERSION, "agentic-review");
    if (existsSync(cached) && checkVersion(cached)) return cached;
  }

  return download(toolCache);
}

// download fetches the pinned release asset for this platform, verifies
// it against the baked checksum constant, installs it into the tool
// cache when $RUNNER_TOOL_CACHE is set (otherwise a scratch temp
// directory), and returns the installed path.
async function download(toolCache) {
  const key = platformKey();
  const expectedSum = CHECKSUMS[key];
  if (!expectedSum) {
    throw new Error(
      `agentic-review-shim: no baked checksum for ${EXPECTED_VERSION} ${key}; ` +
        `set AGENTIC_REVIEW_BIN or bake /usr/local/bin/agentic-review instead ` +
        `until a real release exists`,
    );
  }

  const url =
    `https://github.com/phatblat/agentic-review/releases/download/` +
    `${EXPECTED_VERSION}/agentic-review-${key}`;
  log(`downloading ${url}`);

  const res = await fetch(url);
  if (!res.ok) {
    throw new Error(`agentic-review-shim: download ${url}: HTTP ${res.status}`);
  }
  const bin = Buffer.from(await res.arrayBuffer());

  const actualSum = createHash("sha256").update(bin).digest("hex");
  if (actualSum !== expectedSum) {
    throw new Error(`agentic-review-shim: checksum mismatch for ${url}: want ${expectedSum}, got ${actualSum}`);
  }

  const destDir = toolCache
    ? path.join(toolCache, "agentic-review", EXPECTED_VERSION)
    : path.join(process.env.RUNNER_TEMP || "/tmp", "agentic-review-bin");
  mkdirSync(destDir, { recursive: true });
  const dest = path.join(destDir, "agentic-review");
  writeFileSync(dest, bin);
  chmodSync(dest, 0o755);
  return dest;
}

// applyInputs maps action.yml's INPUT_* env vars (GitHub Actions'
// standard convention: an input named "github_token" arrives as
// $INPUT_GITHUB_TOKEN) onto the env vars cmd/agentic-review/review.go
// reads, then returns the recordDir to pass as --record (empty when the
// `record` input is unset or false).
function applyInputs(env) {
  const token = process.env.INPUT_GITHUB_TOKEN;
  if (token) env.GITHUB_TOKEN = token;

  const configPath = process.env.INPUT_CONFIG_PATH || ".github/agentic-review";
  env.AGENTIC_REVIEW_CONFIG_PATH = configPath;

  if (process.env.INPUT_ENDPOINT) env.AGENTIC_REVIEW_ENDPOINT = process.env.INPUT_ENDPOINT;
  if (process.env.INPUT_API_KEY) env.AGENTIC_REVIEW_API_KEY = process.env.INPUT_API_KEY;
  if (process.env.INPUT_FAIL_ON) env.AGENTIC_REVIEW_FAIL_ON = process.env.INPUT_FAIL_ON;

  let recordDir = "";
  if (process.env.INPUT_RECORD === "true") {
    recordDir = path.join(process.env.RUNNER_TEMP || "/tmp", "agentic-review", "recordings");
  }
  return { configPath, recordDir };
}

async function main() {
  const bin = await resolve();

  const env = { ...process.env };
  const { configPath, recordDir } = applyInputs(env);

  const args = ["review"];
  // In dry-run mode (spec item 9's smoke test) the fixture directory
  // alongside $GITHUB_EVENT_PATH doubles as both the webhook payload
  // and the config/personas source — review.go infers ConfigRoot from
  // it itself, so --config is only passed outside dry-run.
  if (env.AGENTIC_REVIEW_DRY_RUN !== "1") {
    args.push("--config", configPath);
  }
  if (recordDir) args.push("--record", recordDir);

  const r = spawnSync(bin, args, { stdio: "inherit", env });
  if (r.error) {
    log(`spawn ${bin} ${args.join(" ")}: ${r.error.message}`);
    process.exit(1);
  }
  process.exit(r.status === null ? 1 : r.status);
}

main().catch((err) => {
  log(err.message || String(err));
  process.exit(1);
});
