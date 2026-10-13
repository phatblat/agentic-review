package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// versionRecipeFiles are the files `just version` reads or rewrites,
// relative to the repository root.
var versionRecipeFiles = []string{
	"justfile",
	"shim/index.mjs",
	"cmd/agentic-review/main.go",
	"docs/setup.md",
}

const (
	setupDoc       = "docs/setup.md"
	otherRepoPin   = "\n      - uses: actions/checkout@v7.0.1\n        with:\n          repository: other/repo\n          ref: v3.1.4\n"
	shaForTestPins = "0123456789abcdef0123456789abcdef01234567"
)

// TestJustVersion runs the real `just version` recipe against a temp copy of
// the files it touches: the recipe is the only writer of the app version,
// and a regex or docs-layout change would otherwise surface only at the
// next version bump.
func TestJustVersion(t *testing.T) {
	if _, err := exec.LookPath("just"); err != nil {
		t.Skip("just not on PATH")
	}

	t.Run("no argument prints the current version", func(t *testing.T) {
		root := copyVersionTree(t, nil)
		out, err := runJustVersion(t, root)
		if err != nil {
			t.Fatalf("just version: %v\n%s", err, out)
		}
		if out != version+"\n" {
			t.Errorf("output = %q, want %q", out, version+"\n")
		}
	})

	for _, arg := range []string{"1.2.3", "v1.2.3"} {
		t.Run("sets every literal from "+arg, func(t *testing.T) {
			root := copyVersionTree(t, nil)
			if out, err := runJustVersion(t, root, arg); err != nil {
				t.Fatalf("just version %s: %v\n%s", arg, err, out)
			}
			got := readVersionTree(t, root)
			if !strings.Contains(got["shim/index.mjs"], "\nexport const EXPECTED_VERSION = \"v1.2.3\";\n") {
				t.Error("shim/index.mjs: EXPECTED_VERSION is not v1.2.3")
			}
			if !strings.Contains(got["cmd/agentic-review/main.go"], "\nvar version = \"v1.2.3\"\n") {
				t.Error("main.go: version is not v1.2.3")
			}
			if n := strings.Count(got[setupDoc], "ref: v1.2.3"); n != 2 {
				t.Errorf("docs/setup.md: %d ref: v1.2.3 pins, want 2", n)
			}
			if n := strings.Count(got[setupDoc], "ref: "+version); n != 0 {
				t.Errorf("docs/setup.md: %d ref: %s pins left, want 0", n, version)
			}
		})
	}

	t.Run("round trip restores every byte", func(t *testing.T) {
		root := copyVersionTree(t, nil)
		before := readVersionTree(t, root)
		for _, arg := range []string{"9.9.9", version} {
			if out, err := runJustVersion(t, root, arg); err != nil {
				t.Fatalf("just version %s: %v\n%s", arg, err, out)
			}
		}
		if diff := cmp.Diff(before, readVersionTree(t, root)); diff != "" {
			t.Errorf("files after round trip (-before +after):\n%s", diff)
		}
	})

	t.Run("other repos' tag pins are left alone", func(t *testing.T) {
		root := copyVersionTree(t, map[string]func(string) string{
			setupDoc: func(s string) string { return s + otherRepoPin },
		})
		if out, err := runJustVersion(t, root, "9.9.9"); err != nil {
			t.Fatalf("just version 9.9.9: %v\n%s", err, out)
		}
		got := readVersionTree(t, root)[setupDoc]
		if n := strings.Count(got, "ref: v9.9.9"); n != 2 {
			t.Errorf("%d ref: v9.9.9 pins, want 2", n)
		}
		if !strings.HasSuffix(got, otherRepoPin) {
			t.Error("other/repo's ref: v3.1.4 pin was rewritten")
		}
	})

	failures := []struct {
		name string
		arg  string
		edit map[string]func(string) string
	}{
		{name: "two-part version", arg: "1.2"},
		{name: "pre-release version", arg: "1.2.3-rc.1"},
		{name: "double v prefix", arg: "vv1.2.3"},
		{
			name: "main.go without a version literal",
			arg:  "9.9.9",
			edit: map[string]func(string) string{
				"cmd/agentic-review/main.go": func(s string) string {
					return strings.Replace(s, "var version = ", "var appVersion = ", 1)
				},
			},
		},
		{
			name: "an agentic-review pin switched to a SHA",
			arg:  "9.9.9",
			edit: map[string]func(string) string{
				setupDoc: func(s string) string {
					return strings.Replace(s, "ref: "+version, "ref: "+shaForTestPins, 1) + otherRepoPin
				},
			},
		},
	}
	for _, tc := range failures {
		t.Run("rejects "+tc.name+" without writing", func(t *testing.T) {
			root := copyVersionTree(t, tc.edit)
			before := readVersionTree(t, root)
			out, err := runJustVersion(t, root, tc.arg)
			if err == nil {
				t.Fatalf("just version %s succeeded, want failure\n%s", tc.arg, out)
			}
			if diff := cmp.Diff(before, readVersionTree(t, root)); diff != "" {
				t.Errorf("files changed despite failure (-before +after):\n%s", diff)
			}
		})
	}
}

// copyVersionTree copies versionRecipeFiles into a temp repository root,
// passing each file named in edit through its function first.
func copyVersionTree(t *testing.T, edit map[string]func(string) string) string {
	t.Helper()
	root := t.TempDir()
	for _, rel := range versionRecipeFiles {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Fatal(err)
		}
		s := string(b)
		if f := edit[rel]; f != nil {
			s = f(s)
		}
		dst := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// readVersionTree returns the contents of versionRecipeFiles under root.
func readVersionTree(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string, len(versionRecipeFiles))
	for _, rel := range versionRecipeFiles {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		files[rel] = string(b)
	}
	return files
}

// runJustVersion runs `just version [args]` against the justfile under root,
// returning combined stdout and stderr.
func runJustVersion(t *testing.T, root string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("just", append([]string{
		"--justfile", filepath.Join(root, "justfile"),
		"--working-directory", root,
		"version",
	}, args...)...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}
