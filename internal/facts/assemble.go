package facts

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/bmatcuk/doublestar/v4"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/classes/manifest"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/ghevent"
	"github.com/phatblat/agentic-review/internal/gitattributes"
	"github.com/phatblat/agentic-review/internal/logx"
)

// Assemble builds the complete Facts for the pull request named by ev: one
// fresh Port.PullRequest fetch (never trusting the possibly-stale webhook
// payload), one ListFiles call, lazy per-file content reads through store,
// classification of every file, and dependency-change extraction — with a
// semver-derived bump level — from the manifest parsers. It also returns
// the per-path class map so roster.json and the render layer can cite
// individual file classifications, plus the fetched PullRequest and File
// list themselves — callers need both again later (team dispatch,
// post-placement, dedup) and re-fetching them would be a wasted API call.
func Assemble(ctx context.Context, port gh.Port, store *gh.ContentStore, ev *ghevent.Event, cfg *config.Config) (*Facts, map[string]classes.Class, *gh.PullRequest, []gh.File, error) {
	pr, err := port.PullRequest(ctx, ev.Repo, ev.PRNumber)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("facts: fetch pull request: %w", err)
	}

	files, err := port.ListFiles(ctx, ev.Repo, pr.Number)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("facts: list files: %w", err)
	}

	attrs, err := loadGitattributes(ctx, store, pr.HeadSHA)
	if err != nil {
		logx.Debug("facts: .gitattributes unreadable at %s: %v", pr.HeadSHA, err)
	}

	workflowPath := workflowPathFromEnv()

	fileClasses := make(map[string]classes.Class, len(files))
	langAdditions := map[string]int{}
	pathsSet := map[string]bool{}
	classSet := map[string]bool{}
	var (
		additions, deletions, binaryFiles, maxFileAdditions int
		touchesReviewConfig, touchesWorkflows               bool
		depChanges                                          []DepChange
	)

	for _, f := range files {
		headFn := readerFunc(ctx, store, f.Path, pr.HeadSHA)
		baseFn := baseReaderFunc(ctx, store, f.Path, pr.BaseSHA, f.Status)

		cls, _ := classes.Classify(classes.Input{
			File:         f,
			HeadContent:  headFn,
			BaseContent:  baseFn,
			Attributes:   attrs,
			DocsGlobs:    cfg.DocsGlobs,
			WorkflowPath: workflowPath,
		})
		fileClasses[f.Path] = cls
		classSet[string(cls)] = true

		if cls == classes.ClassReviewConfig {
			touchesReviewConfig = true
		}
		if matchesWorkflows(f.Path) {
			touchesWorkflows = true
		}

		additions += f.Additions
		deletions += f.Deletions
		if f.Additions > maxFileAdditions {
			maxFileAdditions = f.Additions
		}
		if f.Patch == "" {
			binaryFiles++
		}
		pathsSet[f.Path] = true
		langAdditions[LanguageOf(f.Path)] += f.Additions

		if cls == classes.ClassDeps {
			depChanges = append(depChanges, extractDepChanges(f.Path, headFn, baseFn)...)
		}
	}

	out := &Facts{
		PR: PR{
			Number:            pr.Number,
			BaseRef:           pr.BaseRef,
			HeadSHA:           pr.HeadSHA,
			IsFork:            pr.IsFork,
			AuthorAssociation: ParseAssoc(pr.AuthorAssociation),
			Labels:            append([]string(nil), pr.Labels...),
			Draft:             pr.Draft,
			Commits:           pr.Commits,
		},
		Diff: Diff{
			FilesChanged:        len(files),
			Additions:           additions,
			Deletions:           deletions,
			Languages:           langAdditions,
			Paths:               sortedSetKeys(pathsSet),
			Classes:             sortedSetKeys(classSet),
			TouchesReviewConfig: touchesReviewConfig,
			TouchesWorkflows:    touchesWorkflows,
			BinaryFiles:         binaryFiles,
			MaxFileAdditions:    maxFileAdditions,
		},
		Deps: Deps{Changed: depChanges},
	}
	return out, fileClasses, pr, files, nil
}

func loadGitattributes(ctx context.Context, store *gh.ContentStore, headSHA string) (*gitattributes.Set, error) {
	data, err := store.Get(ctx, ".gitattributes", headSHA)
	if err != nil {
		return nil, err
	}
	return gitattributes.Parse(data), nil
}

// workflowPathFromEnv derives the repo-relative workflow file path from the
// standard GITHUB_WORKFLOW_REF env var
// ("owner/repo/.github/workflows/review.yml@refs/heads/main"), so
// classes.Classify can recognise a diff touching the invoking workflow
// itself as review-config.
func workflowPathFromEnv() string {
	ref := os.Getenv("GITHUB_WORKFLOW_REF")
	if ref == "" {
		return ""
	}
	if i := strings.Index(ref, "@"); i >= 0 {
		ref = ref[:i]
	}
	if i := strings.Index(ref, ".github/"); i >= 0 {
		return ref[i:]
	}
	return ""
}

func matchesWorkflows(p string) bool {
	ok, _ := doublestar.Match(".github/workflows/**", p)
	return ok
}

// readerFunc closes over store.Get for one path@ref, lazily.
func readerFunc(ctx context.Context, store *gh.ContentStore, path, ref string) func() ([]byte, error) {
	if ref == "" {
		return nil
	}
	return func() ([]byte, error) { return store.Get(ctx, path, ref) }
}

// baseReaderFunc is readerFunc for the base ref, except a newly added file
// has no base-ref content by definition — returning that directly avoids a
// guaranteed-404 round trip and folds into the same "unreadable" handling
// every other read failure gets.
func baseReaderFunc(ctx context.Context, store *gh.ContentStore, path, ref, status string) func() ([]byte, error) {
	if ref == "" || status == "added" {
		return func() ([]byte, error) {
			return nil, fmt.Errorf("facts: %s has no base-ref content (status=%s)", path, status)
		}
	}
	return readerFunc(ctx, store, path, ref)
}

func extractDepChanges(path string, headFn, baseFn func() ([]byte, error)) []DepChange {
	parser := manifest.For(path)
	if parser == nil || headFn == nil || baseFn == nil {
		return nil
	}
	head, err := headFn()
	if err != nil {
		return nil
	}
	base, err := baseFn()
	if err != nil {
		return nil
	}
	ok, changes, err := parser.VersionOnly(base, head)
	if err != nil || !ok {
		return nil
	}
	out := make([]DepChange, 0, len(changes))
	for _, c := range changes {
		out = append(out, DepChange{
			Ecosystem: c.Ecosystem,
			Name:      c.Name,
			From:      c.From,
			To:        c.To,
			Bump:      bumpLevel(c.From, c.To),
			Path:      path,
		})
	}
	return out
}

// bumpLevel classifies a version change by semver level. Either side
// failing to parse as semver (e.g. an npm range like "^1.2.3", or a
// non-numeric tag) yields "other".
func bumpLevel(from, to string) string {
	fv, ferr := semver.NewVersion(from)
	tv, terr := semver.NewVersion(to)
	if ferr != nil || terr != nil {
		return "other"
	}
	switch {
	case fv.Major() != tv.Major():
		return "major"
	case fv.Minor() != tv.Minor():
		return "minor"
	case fv.Patch() != tv.Patch():
		return "patch"
	case fv.Prerelease() != tv.Prerelease() || fv.Metadata() != tv.Metadata():
		return "prerelease"
	default:
		return "other"
	}
}

func sortedSetKeys(set map[string]bool) []string {
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
