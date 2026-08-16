package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/bmatcuk/doublestar/v4"

	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
)

// PRContext is the untrusted PR content available to every turn: title,
// body, and commit messages.
type PRContext struct {
	Title   string
	Body    string
	Commits []string
}

// buildScopedInput assembles rp's user-message content per its
// inputs.scope and inputs.context (spec item 32).
func buildScopedInput(ctx context.Context, rp *persona.ResolvedPersona, f *facts.Facts, files []gh.File, pr PRContext, store *gh.ContentStore, baseSHA, headSHA string) (string, error) {
	scope := rp.Inputs.Scope
	if scope == "" {
		scope = "metadata-only"
	}

	var b strings.Builder

	factsJSON, err := json.Marshal(f)
	if err != nil {
		return "", fmt.Errorf("runner: %s: marshal facts: %w", rp.ID, err)
	}
	b.WriteString("Facts (runtime-assembled; trusted):\n")
	b.Write(factsJSON)
	b.WriteString("\n\n")

	scopedFiles := scopedFileSet(rp, f, files)

	switch scope {
	case "metadata-only":
		// no diff content
	case "full-diff", "matched-files":
		b.WriteString(renderDiffBlock(scopedFiles))
		b.WriteString("\n\n")
	case "full-files":
		text, err := renderFullFilesBlock(ctx, store, headSHA, scopedFiles)
		if err != nil {
			return "", err
		}
		b.WriteString(text)
		b.WriteString("\n\n")
	default:
		return "", fmt.Errorf("runner: %s: unknown inputs.scope %q", rp.ID, scope)
	}

	for _, block := range rp.Inputs.Context {
		text, err := renderContextBlock(ctx, block, pr, store, baseSHA, headSHA, scopedFiles)
		if err != nil {
			return "", err
		}
		if text != "" {
			b.WriteString(text)
			b.WriteString("\n\n")
		}
	}

	return strings.TrimRight(b.String(), "\n") + "\n", nil
}

// scopedFileSet resolves which files a matched-files-scoped persona's turn
// covers. Every other scope simply gets every changed file.
func scopedFileSet(rp *persona.ResolvedPersona, f *facts.Facts, files []gh.File) []gh.File {
	if rp.Inputs.Scope != "matched-files" {
		return files
	}
	matched, ok := matchedPaths(rp, f.Diff.Paths)
	if !ok {
		logx.Debug("runner: %s: matched-files scope but activation has no path glob; falling back to full-diff", rp.ID)
		return files
	}
	out := make([]gh.File, 0, len(matched))
	for _, file := range files {
		if matched[file.Path] {
			out = append(out, file)
		}
	}
	return out
}

// matchedPaths reports the union of files matching any Paths glob across
// rp's volunteer_on trigger groups. ok is false when no group has a Paths
// field at all — the persona activated on a non-path trigger (domains,
// labels, expr) and so has no matched path set.
func matchedPaths(rp *persona.ResolvedPersona, allPaths []string) (map[string]bool, bool) {
	hasPathGlob := false
	matched := map[string]bool{}
	for _, group := range rp.Activation.VolunteerOn {
		if len(group.Paths) == 0 {
			continue
		}
		hasPathGlob = true
		for _, p := range allPaths {
			for _, glob := range group.Paths {
				if ok, _ := doublestar.Match(glob, p); ok {
					matched[p] = true
					break
				}
			}
		}
	}
	if !hasPathGlob {
		return nil, false
	}
	return matched, true
}

func renderDiffBlock(files []gh.File) string {
	var b strings.Builder
	b.WriteString("Diff hunks:\n")
	for _, f := range files {
		if f.Patch == "" {
			continue
		}
		b.WriteString(infer.WrapUntrusted("diff:"+f.Path, f.Patch))
		b.WriteString("\n")
	}
	return b.String()
}

func renderFullFilesBlock(ctx context.Context, store *gh.ContentStore, headSHA string, files []gh.File) (string, error) {
	var b strings.Builder
	b.WriteString("Full file contents at head_sha:\n")
	for _, f := range files {
		content, err := store.Get(ctx, f.Path, headSHA)
		if err != nil {
			logx.Debug("runner: full-files: %s: %v", f.Path, err)
			continue
		}
		b.WriteString(infer.WrapUntrusted("file-head:"+f.Path, string(content)))
		b.WriteString("\n")
	}
	return b.String(), nil
}

func renderContextBlock(ctx context.Context, block string, pr PRContext, store *gh.ContentStore, baseSHA, headSHA string, files []gh.File) (string, error) {
	switch block {
	case "pr-metadata":
		return "", nil // already covered by the facts JSON above
	case "pr-body":
		return infer.WrapUntrusted("pr-title", pr.Title) + "\n\n" + infer.WrapUntrusted("pr-body", pr.Body), nil
	case "commit-messages":
		if len(pr.Commits) == 0 {
			return "", nil
		}
		return infer.WrapUntrusted("commit-messages", strings.Join(pr.Commits, "\n---\n")), nil
	case "file-contents-head":
		text, err := renderFullFilesBlock(ctx, store, headSHA, files)
		return text, err
	case "file-contents-base":
		var b strings.Builder
		for _, f := range files {
			content, err := store.Get(ctx, f.Path, baseSHA)
			if err != nil {
				logx.Debug("runner: file-contents-base: %s: %v", f.Path, err)
				continue
			}
			b.WriteString(infer.WrapUntrusted("file-base:"+f.Path, string(content)))
			b.WriteString("\n")
		}
		return b.String(), nil
	default:
		return "", fmt.Errorf("unknown inputs.context block %q", block)
	}
}
