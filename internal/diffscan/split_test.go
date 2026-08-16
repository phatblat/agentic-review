package diffscan

import "testing"

const sampleMultiFileDiff = `diff --git a/src/main.go b/src/main.go
index 1111111..2222222 100644
--- a/src/main.go
+++ b/src/main.go
@@ -1,3 +1,4 @@
 package main
+// added
 func main() {}
diff --git a/README.md b/README.md
index 3333333..4444444 100644
--- a/README.md
+++ b/README.md
@@ -1,1 +1,2 @@
 # Title
+more text
`

func TestSplitFiles(t *testing.T) {
	files := SplitFiles(sampleMultiFileDiff)
	if len(files) != 2 {
		t.Fatalf("SplitFiles = %d files, want 2", len(files))
	}
	if files[0].Path != "src/main.go" || files[1].Path != "README.md" {
		t.Errorf("paths = %q, %q", files[0].Path, files[1].Path)
	}

	cov := Scan(files[0].Patch)
	if !cov.Added[2] {
		t.Errorf("expected line 2 of src/main.go to be an added line; cov=%+v", cov)
	}
}

func TestSplitFilesEmpty(t *testing.T) {
	if files := SplitFiles(""); len(files) != 0 {
		t.Errorf("SplitFiles(\"\") = %+v, want empty", files)
	}
}

func TestCountChanges(t *testing.T) {
	patch := "@@ -1,2 +1,3 @@\n" +
		"--- a/f\n" +
		"+++ b/f\n" +
		" ctx\n" +
		"-old\n" +
		"+new1\n" +
		"+new2\n"
	additions, deletions := CountChanges(patch)
	if additions != 2 || deletions != 1 {
		t.Errorf("additions=%d deletions=%d, want 2/1", additions, deletions)
	}
}

func TestCountChangesIgnoresFileHeaders(t *testing.T) {
	patch := "--- a/f\n+++ b/f\n@@ -1,1 +1,1 @@\n-x\n+y\n"
	additions, deletions := CountChanges(patch)
	if additions != 1 || deletions != 1 {
		t.Errorf("additions=%d deletions=%d, want 1/1 (file headers must not count)", additions, deletions)
	}
}
