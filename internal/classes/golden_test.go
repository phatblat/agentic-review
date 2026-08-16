package classes_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/goldentest"
)

// goldenResult is want.json's shape (spec item 51).
type goldenResult struct {
	Class  string `json:"class"`
	Reason string `json:"reason"`
}

// TestGolden classifies the single changed file in every
// fixtures/classes/<ecosystem>/<case>/ directory and compares against
// want.json (spec item 51, Verification item 2). The changed file's
// relative path is discovered from head/ (each fixture case has exactly
// one target file, mirrored under base/ and head/ at the same path).
func TestGolden(t *testing.T) {
	roots, err := filepath.Glob("../../fixtures/classes/*/*")
	if err != nil {
		t.Fatalf("glob fixture roots: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("no fixtures found under fixtures/classes/*/*")
	}

	for _, root := range roots {
		root := root
		t.Run(filepath.Base(filepath.Dir(root))+"/"+filepath.Base(root), func(t *testing.T) {
			relPath := findTargetFile(t, filepath.Join(root, "head"))

			in := classes.Input{
				File: gh.File{Path: relPath},
				HeadContent: func() ([]byte, error) {
					return os.ReadFile(filepath.Join(root, "head", relPath))
				},
				BaseContent: func() ([]byte, error) {
					return os.ReadFile(filepath.Join(root, "base", relPath))
				},
			}

			cls, reason := classes.Classify(in)
			goldentest.JSON(t, filepath.Join(root, "want.json"), goldenResult{Class: string(cls), Reason: reason})
		})
	}
}

// findTargetFile returns the single file under dir, relative to dir.
func findTargetFile(t *testing.T, dir string) string {
	t.Helper()
	var found string
	err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			rel, relErr := filepath.Rel(dir, path)
			if relErr != nil {
				return relErr
			}
			found = filepath.ToSlash(rel)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", dir, err)
	}
	if found == "" {
		t.Fatalf("no file found under %s", dir)
	}
	return found
}
