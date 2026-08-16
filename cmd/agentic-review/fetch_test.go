package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestParseFetchURL(t *testing.T) {
	tests := []struct {
		name                string
		url                 string
		wantOwner, wantRepo string
		wantPR              int
		wantRun             int64
		wantErr             bool
	}{
		{
			name: "pull request URL", url: "https://github.com/phatblat/agentic-review/pull/42",
			wantOwner: "phatblat", wantRepo: "agentic-review", wantPR: 42,
		},
		{
			name: "workflow run URL", url: "https://github.com/phatblat/agentic-review/actions/runs/17283",
			wantOwner: "phatblat", wantRepo: "agentic-review", wantRun: 17283,
		},
		{
			name: "job URL (run id extracted, job id ignored)", url: "https://github.com/phatblat/agentic-review/actions/runs/17283/job/48231",
			wantOwner: "phatblat", wantRepo: "agentic-review", wantRun: 17283,
		},
		{name: "not a github URL", url: "https://example.com/foo/bar/pull/1", wantErr: true},
		{name: "github URL missing pull/actions segment", url: "https://github.com/phatblat/agentic-review", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, pr, run, err := parseFetchURL(tt.url)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parseFetchURL(%q) succeeded, want an error", tt.url)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFetchURL(%q): %v", tt.url, err)
			}
			if owner != tt.wantOwner || repo != tt.wantRepo || pr != tt.wantPR || run != tt.wantRun {
				t.Errorf("parseFetchURL(%q) = (%q, %q, %d, %d), want (%q, %q, %d, %d)",
					tt.url, owner, repo, pr, run, tt.wantOwner, tt.wantRepo, tt.wantPR, tt.wantRun)
			}
		})
	}
}

func TestUnzipToExtractsNestedFiles(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	files := map[string]string{
		"triage.json":        `{"risk":"high"}`,
		"recordings/logic.0": "verbatim request/response",
	}
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%s): %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}

	dest := filepath.Join(t.TempDir(), "pr-7-run-42")
	if err := unzipTo(buf.Bytes(), dest); err != nil {
		t.Fatalf("unzipTo: %v", err)
	}

	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dest, name))
		if err != nil {
			t.Fatalf("read extracted %s: %v", name, err)
		}
		if string(got) != want {
			t.Errorf("extracted %s = %q, want %q", name, got, want)
		}
	}
}

// TestUnzipToRejectsPathTraversal confirms a malicious archive entry
// naming "../../etc/passwd" cannot escape the destination directory.
func TestUnzipToRejectsPathTraversal(t *testing.T) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, err := zw.Create("../../escaped.txt")
	if err != nil {
		t.Fatalf("zip.Create: %v", err)
	}
	if _, err := w.Write([]byte("should stay contained")); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip.Close: %v", err)
	}

	parent := t.TempDir()
	dest := filepath.Join(parent, "dest")
	if err := unzipTo(buf.Bytes(), dest); err != nil {
		t.Fatalf("unzipTo: %v", err)
	}

	if _, err := os.Stat(filepath.Join(parent, "escaped.txt")); err == nil {
		t.Fatal("archive entry escaped the destination directory")
	}
	if _, err := os.Stat(filepath.Join(dest, "escaped.txt")); err != nil {
		t.Errorf("extracted file not contained in dest: %v", err)
	}
}
