package diffscan

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/gh"
)

func TestScanSingleHunk(t *testing.T) {
	// 3 context lines, 2 additions, 1 deletion.
	patch := "@@ -10,4 +10,5 @@ func foo() {\n" +
		" line10\n" +
		"-line11-old\n" +
		"+line11-new\n" +
		"+line12-new\n" +
		" line13\n" +
		" line14\n"

	cov := Scan(patch)

	if len(cov.Hunks) != 1 {
		t.Fatalf("Hunks = %d, want 1", len(cov.Hunks))
	}
	want := Hunk{OldStart: 10, OldLines: 4, NewStart: 10, NewLines: 5}
	if cov.Hunks[0] != want {
		t.Errorf("Hunks[0] = %+v, want %+v", cov.Hunks[0], want)
	}

	// New-side numbering: 10=context, 11=added, 12=added, 13=context,
	// 14=context. The deleted old-side line does not appear on the new side.
	for _, ln := range []int{10, 11, 12, 13, 14} {
		if !cov.Right[ln] {
			t.Errorf("Right[%d] = false, want true", ln)
		}
	}
	if !cov.Added[11] || !cov.Added[12] {
		t.Errorf("Added = %v, want {11,12}", cov.Added)
	}
	if cov.Added[10] || cov.Added[13] || cov.Added[14] {
		t.Errorf("Added incorrectly includes a context line: %v", cov.Added)
	}

	// Old-side numbering: 10=context, 11=deleted, 12=context(new13),
	// 13=context(new14).
	for _, ln := range []int{10, 11, 12, 13} {
		if !cov.Left[ln] {
			t.Errorf("Left[%d] = false, want true", ln)
		}
	}
	if cov.Left[14] {
		t.Errorf("Left incorrectly includes line past the old hunk: %v", cov.Left)
	}
}

func TestScanMultipleHunksAndOmittedCounts(t *testing.T) {
	// Omitted ",N" defaults to a 1-line hunk on that side.
	patch := "@@ -5 +5 @@\n" +
		"+solo-add\n" +
		"@@ -20,2 +21,2 @@\n" +
		" ctx\n" +
		"+added\n"

	cov := Scan(patch)

	if len(cov.Hunks) != 2 {
		t.Fatalf("Hunks = %d, want 2", len(cov.Hunks))
	}
	if got, want := cov.Hunks[0], (Hunk{OldStart: 5, OldLines: 1, NewStart: 5, NewLines: 1}); got != want {
		t.Errorf("Hunks[0] = %+v, want %+v", got, want)
	}
	if got, want := cov.Hunks[1], (Hunk{OldStart: 20, OldLines: 2, NewStart: 21, NewLines: 2}); got != want {
		t.Errorf("Hunks[1] = %+v, want %+v", got, want)
	}

	if !cov.Added[5] {
		t.Errorf("Added missing line 5 from the first hunk")
	}
	if !cov.Right[21] || !cov.Added[22] {
		t.Errorf("second hunk not scanned correctly: Right=%v Added=%v", cov.Right, cov.Added)
	}
}

func TestScanNoNewlineMarkerAdvancesNeitherSide(t *testing.T) {
	patch := "@@ -1,1 +1,1 @@\n" +
		"-old\n" +
		"+new\n" +
		"\\ No newline at end of file\n"

	cov := Scan(patch)

	if !cov.Added[1] {
		t.Errorf("Added = %v, want line 1 present", cov.Added)
	}
	// The marker line must not have advanced newLine past 1, and must not
	// itself register as covering any line.
	if len(cov.Right) != 1 || len(cov.Left) != 1 {
		t.Errorf("marker line should not add coverage: Right=%v Left=%v", cov.Right, cov.Left)
	}
}

func TestScanSkipsPreambleBeforeFirstHunk(t *testing.T) {
	patch := "diff --git a/f b/f\n" +
		"index 000..111 100644\n" +
		"--- a/f\n" +
		"+++ b/f\n" +
		"@@ -1,1 +1,1 @@\n" +
		"-a\n" +
		"+b\n"

	cov := Scan(patch)
	if len(cov.Hunks) != 1 || !cov.Added[1] {
		t.Fatalf("preamble lines were not skipped: %+v", cov)
	}
}

func TestScanEmptyPatch(t *testing.T) {
	cov := Scan("")
	if len(cov.Hunks) != 0 || len(cov.Added) != 0 || len(cov.Right) != 0 || len(cov.Left) != 0 {
		t.Errorf("Scan(\"\") = %+v, want all empty", cov)
	}
}

func TestScanFilesSkipsEmptyPatch(t *testing.T) {
	files := []gh.File{
		{Path: "a.go", Patch: "@@ -1,1 +1,1 @@\n-a\n+b\n"},
		{Path: "binary.png", Patch: ""},
	}
	out := ScanFiles(files)
	if _, ok := out["a.go"]; !ok {
		t.Errorf("ScanFiles dropped a.go")
	}
	if _, ok := out["binary.png"]; ok {
		t.Errorf("ScanFiles included binary.png, which has no patch")
	}
	if len(out) != 1 {
		t.Errorf("ScanFiles returned %d entries, want 1", len(out))
	}
}
