package runner

import (
	"context"
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/persona"
)

func scopedRP(scope string, ctxBlocks []string) *persona.ResolvedPersona {
	return &persona.ResolvedPersona{Definition: persona.Definition{
		ID:     "p",
		Inputs: persona.Inputs{Scope: scope, Context: ctxBlocks},
	}}
}

var scopeFiles = []gh.File{
	{Path: "src/main.go", Patch: "@@ -1,1 +1,1 @@\n-old\n+new\n"},
	{Path: "docs/readme.md", Patch: "@@ -1,1 +1,1 @@\n-old doc\n+new doc\n"},
}

func TestBuildScopedInputMetadataOnlyOmitsDiff(t *testing.T) {
	rp := scopedRP("metadata-only", nil)
	out, err := buildScopedInput(context.Background(), rp, &facts.Facts{}, scopeFiles, PRContext{}, nil, "base", "head")
	if err != nil {
		t.Fatalf("buildScopedInput: %v", err)
	}
	if strings.Contains(out, "Diff hunks:") || strings.Contains(out, "main.go") {
		t.Errorf("metadata-only output contains diff content: %q", out)
	}
	if !strings.Contains(out, "Facts (runtime-assembled") {
		t.Errorf("metadata-only output missing facts block: %q", out)
	}
}

func TestBuildScopedInputFullDiffIncludesEveryFile(t *testing.T) {
	rp := scopedRP("full-diff", nil)
	out, err := buildScopedInput(context.Background(), rp, &facts.Facts{}, scopeFiles, PRContext{}, nil, "base", "head")
	if err != nil {
		t.Fatalf("buildScopedInput: %v", err)
	}
	hunks := out[strings.Index(out, "Diff hunks:"):]
	if !strings.Contains(hunks, "-old\n+new") || !strings.Contains(hunks, "-old doc\n+new doc") {
		t.Errorf("full-diff output missing a changed file's patch: %q", hunks)
	}
}

func TestBuildScopedInputMatchedFilesFiltersToGlob(t *testing.T) {
	rp := scopedRP("matched-files", nil)
	rp.Activation.VolunteerOn = []activation.Trigger{{Paths: []string{"src/**"}}}
	f := &facts.Facts{Diff: facts.Diff{Paths: []string{"src/main.go", "docs/readme.md"}}}

	out, err := buildScopedInput(context.Background(), rp, f, scopeFiles, PRContext{}, nil, "base", "head")
	if err != nil {
		t.Fatalf("buildScopedInput: %v", err)
	}
	// The facts JSON block always echoes every diff path regardless of
	// scope, so assert against the diff-hunks section specifically.
	hunks := out[strings.Index(out, "Diff hunks:"):]
	if !strings.Contains(hunks, "old\n+new") {
		t.Errorf("matched-files diff hunks missing the matched file's patch: %q", hunks)
	}
	if strings.Contains(hunks, "old doc") {
		t.Errorf("matched-files diff hunks include an unmatched file's patch: %q", hunks)
	}
}

func TestBuildScopedInputMatchedFilesFallsBackWithoutPathGlob(t *testing.T) {
	rp := scopedRP("matched-files", nil)
	// Activated on a non-path trigger (domains) — no Paths glob anywhere,
	// so scopedFileSet must fall back to every changed file (spec item 32).
	rp.Activation.VolunteerOn = []activation.Trigger{{Domains: []string{"auth"}}}
	f := &facts.Facts{Diff: facts.Diff{Paths: []string{"src/main.go", "docs/readme.md"}}}

	out, err := buildScopedInput(context.Background(), rp, f, scopeFiles, PRContext{}, nil, "base", "head")
	if err != nil {
		t.Fatalf("buildScopedInput: %v", err)
	}
	hunks := out[strings.Index(out, "Diff hunks:"):]
	if !strings.Contains(hunks, "-old\n+new") || !strings.Contains(hunks, "-old doc\n+new doc") {
		t.Errorf("fallback should include every file's patch, got: %q", hunks)
	}
}

func TestBuildScopedInputUnknownScopeErrors(t *testing.T) {
	rp := scopedRP("bogus-scope", nil)
	if _, err := buildScopedInput(context.Background(), rp, &facts.Facts{}, scopeFiles, PRContext{}, nil, "base", "head"); err == nil {
		t.Fatalf("buildScopedInput succeeded with an unknown scope, want an error")
	}
}

func TestBuildScopedInputContextBlocksAppendPRMetadata(t *testing.T) {
	rp := scopedRP("metadata-only", []string{"pr-body", "commit-messages"})
	pr := PRContext{Title: "Fix the bug", Body: "Detailed body", Commits: []string{"fix: the bug", "chore: cleanup"}}

	out, err := buildScopedInput(context.Background(), rp, &facts.Facts{}, nil, pr, nil, "base", "head")
	if err != nil {
		t.Fatalf("buildScopedInput: %v", err)
	}
	if !strings.Contains(out, "Fix the bug") || !strings.Contains(out, "Detailed body") {
		t.Errorf("output missing pr-body context block: %q", out)
	}
	if !strings.Contains(out, "fix: the bug") || !strings.Contains(out, "chore: cleanup") {
		t.Errorf("output missing commit-messages context block: %q", out)
	}
}
