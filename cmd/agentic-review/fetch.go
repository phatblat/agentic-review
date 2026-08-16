package main

import (
	"archive/zip"
	"bytes"
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/google/go-github/v75/github"
)

func init() {
	commands["fetch"] = cmdFetch
}

// urlRE matches a GitHub PR, workflow run, or job URL and captures owner,
// repo, and either the PR number (group "pr") or the run id (group
// "run"); a job URL's /job/<id> suffix is ignored — the run id alone
// resolves the artifact.
var urlRE = regexp.MustCompile(
	`^https://github\.com/([^/]+)/([^/]+)/(?:pull/(?P<pr>\d+)|actions/runs/(?P<run>\d+))`,
)

// cmdFetch implements `agentic-review fetch <pr|run|job url> [--out
// fixtures/] [--run <id>]` (spec §13.2, plan item 50): resolves the URL
// to owner/repo plus a PR or run id, finds the newest workflow run for
// that PR's head SHA (or the run id directly / --run's override), locates
// the agentic-review-run artifact dogfood.yml uploads, downloads and
// unzips it to <out>/pr-<N>-run-<M>/.
func cmdFetch(ctx context.Context, args []string) int {
	fs := flag.NewFlagSet("fetch", flag.ContinueOnError)
	out := fs.String("out", "fixtures", "destination prefix; the run unpacks to <out>/pr-<N>-run-<M>/")
	runOverride := fs.Int64("run", 0, "use this workflow run id instead of the newest run for the PR's head SHA")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(os.Stderr, "fetch: a single <pr|run|job url> argument is required")
		return 1
	}

	owner, repo, prNumber, runID, err := parseFetchURL(fs.Arg(0))
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: %v\n", err)
		return 1
	}

	token, err := resolveToken()
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: %v\n", err)
		return 1
	}
	client := github.NewClient(nil).WithAuthToken(token)

	if runOverride != nil && *runOverride != 0 {
		runID = *runOverride
	}
	if runID == 0 {
		// A PR URL: resolve its head SHA, then the newest workflow run
		// against that SHA (GitHub returns workflow_runs newest first).
		pr, _, err := client.PullRequests.Get(ctx, owner, repo, prNumber)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch: get pull request %s/%s#%d: %v\n", owner, repo, prNumber, err)
			return 1
		}
		runs, _, err := client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, &github.ListWorkflowRunsOptions{
			HeadSHA: pr.GetHead().GetSHA(),
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch: list workflow runs for %s: %v\n", pr.GetHead().GetSHA(), err)
			return 1
		}
		if len(runs.WorkflowRuns) == 0 {
			fmt.Fprintf(os.Stderr, "fetch: no workflow runs found for %s/%s#%d's head SHA %s\n", owner, repo, prNumber, pr.GetHead().GetSHA())
			return 1
		}
		sort.Slice(runs.WorkflowRuns, func(i, j int) bool {
			return runs.WorkflowRuns[i].GetCreatedAt().After(runs.WorkflowRuns[j].GetCreatedAt().Time)
		})
		runID = runs.WorkflowRuns[0].GetID()
	}
	if prNumber == 0 {
		// A run/job URL: recover the PR number from the run itself so
		// the destination directory name still carries it.
		run, _, err := client.Actions.GetWorkflowRunByID(ctx, owner, repo, runID)
		if err != nil {
			fmt.Fprintf(os.Stderr, "fetch: get workflow run %d: %v\n", runID, err)
			return 1
		}
		if len(run.PullRequests) == 0 {
			fmt.Fprintf(os.Stderr, "fetch: workflow run %d is not associated with any pull request\n", runID)
			return 1
		}
		prNumber = run.PullRequests[0].GetNumber()
	}

	artifacts, _, err := client.Actions.ListWorkflowRunArtifacts(ctx, owner, repo, runID, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: list artifacts for run %d: %v\n", runID, err)
		return 1
	}
	var artifactID int64
	found := false
	for _, a := range artifacts.Artifacts {
		if a.GetName() == "agentic-review-run" {
			artifactID = a.GetID()
			found = true
			break
		}
	}
	if !found {
		fmt.Fprintf(os.Stderr, "fetch: run %d has no %q artifact\n", runID, "agentic-review-run")
		return 1
	}

	dlURL, _, err := client.Actions.DownloadArtifact(ctx, owner, repo, artifactID, 0)
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: resolve download URL for artifact %d: %v\n", artifactID, err)
		return 1
	}
	zipData, err := downloadURL(dlURL.String())
	if err != nil {
		fmt.Fprintf(os.Stderr, "fetch: download artifact: %v\n", err)
		return 1
	}

	dest := filepath.Join(*out, fmt.Sprintf("pr-%d-run-%d", prNumber, runID))
	if err := unzipTo(zipData, dest); err != nil {
		fmt.Fprintf(os.Stderr, "fetch: unzip to %s: %v\n", dest, err)
		return 1
	}

	_, _ = fmt.Fprintf(os.Stdout, "fetch: unpacked %s/%s run %d into %s\n", owner, repo, runID, dest)
	return 0
}

// parseFetchURL extracts owner, repo, and either a PR number or a
// workflow run id from a PR, run, or job URL. Exactly one of prNumber,
// runID is non-zero on success.
func parseFetchURL(raw string) (owner, repo string, prNumber int, runID int64, err error) {
	m := urlRE.FindStringSubmatch(raw)
	if m == nil {
		return "", "", 0, 0, fmt.Errorf("%q is not a github.com pull request, workflow run, or job URL", raw)
	}
	owner, repo = m[1], m[2]
	if pr := m[urlRE.SubexpIndex("pr")]; pr != "" {
		n, _ := strconv.Atoi(pr)
		return owner, repo, n, 0, nil
	}
	run := m[urlRE.SubexpIndex("run")]
	id, _ := strconv.ParseInt(run, 10, 64)
	return owner, repo, 0, id, nil
}

// resolveToken honors $GITHUB_TOKEN, falling back to `gh auth token`
// (spec item 50: "auth from GITHUB_TOKEN else gh auth token").
func resolveToken() (string, error) {
	if t := os.Getenv("GITHUB_TOKEN"); t != "" {
		return t, nil
	}
	out, err := exec.Command("gh", "auth", "token").Output()
	if err != nil {
		return "", fmt.Errorf("no $GITHUB_TOKEN and `gh auth token` failed: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

// downloadURL fetches an artifact's pre-signed blob-storage URL with a
// plain, unauthenticated client — GitHub's redirect target does not
// accept (and can reject) the API token.
func downloadURL(u string) ([]byte, error) {
	resp, err := http.Get(u) //nolint:gosec,noctx // u comes from DownloadArtifact's own redirect resolution, not user input
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: HTTP %d", u, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// unzipTo extracts a zip archive's contents into dir, creating it (and
// any parents) as needed. Entries are path-joined and cleaned so a
// malicious archive cannot escape dir via "../" traversal.
func unzipTo(data []byte, dir string) error {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, f := range r.File {
		target := filepath.Join(dir, filepath.Clean(string(filepath.Separator)+f.Name))
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			_ = rc.Close()
			return err
		}
		_, copyErr := io.Copy(w, rc)
		_ = rc.Close()
		_ = w.Close()
		if copyErr != nil {
			return copyErr
		}
	}
	return nil
}
