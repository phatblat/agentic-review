package facts_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/ghevent"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestAssemble(t *testing.T) {
	dir := t.TempDir()

	writeFile(t, filepath.Join(dir, "pr.json"), `{
		"number": 7, "base_ref": "main", "base_sha": "basesha", "head_ref": "feature",
		"head_sha": "headsha", "is_fork": true, "author_association": "FIRST_TIME_CONTRIBUTOR",
		"labels": ["deps"], "draft": false, "commits": 2
	}`)
	writeFile(t, filepath.Join(dir, "files.json"), `[
		{"path": "Cargo.toml", "status": "modified", "additions": 1, "deletions": 1,
		 "patch": "@@ -1,1 +1,1 @@\n-openssl = \"1.0.2\"\n+openssl = \"3.2.1\"\n"},
		{"path": "src/main.rs", "status": "modified", "additions": 5, "deletions": 1,
		 "patch": "@@ -1,1 +1,5 @@\n-old\n+new1\n+new2\n+new3\n+new4\n+new5\n"},
		{"path": "docs/guide.md", "status": "added", "additions": 3, "deletions": 0,
		 "patch": "@@ -0,0 +1,3 @@\n+line1\n+line2\n+line3\n"}
	]`)

	writeFile(t, filepath.Join(dir, "base", "Cargo.toml"), "[dependencies]\nopenssl = \"1.0.2\"\n")
	writeFile(t, filepath.Join(dir, "head", "Cargo.toml"), "[dependencies]\nopenssl = \"3.2.1\"\n")
	writeFile(t, filepath.Join(dir, "head", "src", "main.rs"), "new1\nnew2\nnew3\nnew4\nnew5\n")
	writeFile(t, filepath.Join(dir, "head", "docs", "guide.md"), "line1\nline2\nline3\n")

	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	store := gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"})
	ev := &ghevent.Event{Repo: gh.Repo{Owner: "acme", Name: "demo"}, PRNumber: 7}
	cfg := config.Defaults()

	f, fileClasses, pr, files, err := facts.Assemble(context.Background(), fake, store, ev, cfg)
	if err != nil {
		t.Fatalf("Assemble: %v", err)
	}

	if pr.Number != 7 || pr.HeadSHA != "headsha" {
		t.Errorf("returned pr = %+v, want the fetched PullRequest", pr)
	}
	if len(files) != 3 {
		t.Errorf("returned files has %d entries, want 3 (matching files.json)", len(files))
	}
	if f.PR.Number != 7 || f.PR.BaseRef != "main" || f.PR.HeadSHA != "headsha" {
		t.Errorf("PR facts = %+v", f.PR)
	}
	if !f.PR.IsFork {
		t.Errorf("IsFork = false, want true")
	}
	if f.PR.AuthorAssociation != facts.AssocFirstTimeContributor {
		t.Errorf("AuthorAssociation = %v, want AssocFirstTimeContributor", f.PR.AuthorAssociation)
	}

	if f.Diff.FilesChanged != 3 {
		t.Errorf("FilesChanged = %d, want 3", f.Diff.FilesChanged)
	}
	if f.Diff.Additions != 9 || f.Diff.Deletions != 2 {
		t.Errorf("Additions/Deletions = %d/%d, want 9/2", f.Diff.Additions, f.Diff.Deletions)
	}
	if f.Diff.MaxFileAdditions != 5 {
		t.Errorf("MaxFileAdditions = %d, want 5", f.Diff.MaxFileAdditions)
	}

	if fileClasses["Cargo.toml"] != classes.ClassDeps {
		t.Errorf("Cargo.toml class = %s, want deps", fileClasses["Cargo.toml"])
	}
	if fileClasses["docs/guide.md"] != classes.ClassDocs {
		t.Errorf("docs/guide.md class = %s, want docs", fileClasses["docs/guide.md"])
	}
	if fileClasses["src/main.rs"] != classes.ClassSource {
		t.Errorf("src/main.rs class = %s, want source", fileClasses["src/main.rs"])
	}

	wantClasses := []string{"deps", "docs", "source"}
	if len(f.Diff.Classes) != len(wantClasses) {
		t.Fatalf("Diff.Classes = %v, want %v", f.Diff.Classes, wantClasses)
	}
	for i, c := range wantClasses {
		if f.Diff.Classes[i] != c {
			t.Errorf("Diff.Classes[%d] = %s, want %s", i, f.Diff.Classes[i], c)
		}
	}

	if f.Diff.Languages["rust"] != 5 {
		t.Errorf("Languages[rust] = %d, want 5", f.Diff.Languages["rust"])
	}
	if f.Diff.Languages["markdown"] != 3 {
		t.Errorf("Languages[markdown] = %d, want 3", f.Diff.Languages["markdown"])
	}
	if f.Diff.Languages["toml"] != 1 {
		t.Errorf("Languages[toml] = %d, want 1", f.Diff.Languages["toml"])
	}

	if len(f.Deps.Changed) != 1 {
		t.Fatalf("Deps.Changed = %+v, want 1 entry", f.Deps.Changed)
	}
	dc := f.Deps.Changed[0]
	if dc.Ecosystem != "cargo" || dc.Name != "openssl" || dc.From != "1.0.2" || dc.To != "3.2.1" || dc.Bump != "major" {
		t.Errorf("Deps.Changed[0] = %+v, want cargo openssl 1.0.2->3.2.1 major", dc)
	}
}
