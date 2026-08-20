package artifact

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/verify"
)

func TestNewOutOverridesEverything(t *testing.T) {
	t.Setenv("RUNNER_TEMP", "/should/not/be/used")
	dir := filepath.Join(t.TempDir(), "custom-out")
	w, err := New(dir)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if w.Dir() != dir {
		t.Errorf("Dir() = %q, want %q", w.Dir(), dir)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("directory was not created: %v", err)
	}
}

func TestNewUsesRunnerTempWhenSet(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("RUNNER_TEMP", tmp)
	w, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(tmp, "agentic-review")
	if w.Dir() != want {
		t.Errorf("Dir() = %q, want %q", w.Dir(), want)
	}
}

func TestNewFallsBackToDotAgenticReviewRun(t *testing.T) {
	t.Setenv("RUNNER_TEMP", "")
	cwd := t.TempDir()
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(cwd); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(oldWd) })

	w, err := New("")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(".agentic-review", "run")
	if w.Dir() != want {
		t.Errorf("Dir() = %q, want %q", w.Dir(), want)
	}
	if _, err := os.Stat(filepath.Join(cwd, ".agentic-review", "run")); err != nil {
		t.Errorf("directory was not created relative to cwd: %v", err)
	}
}

func TestRecordingsDirIsSubdirOfDir(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	want := filepath.Join(w.Dir(), "recordings")
	if w.RecordingsDir() != want {
		t.Errorf("RecordingsDir() = %q, want %q", w.RecordingsDir(), want)
	}
}

func TestWriteTriageIndentedJSON(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	a := &schema.Assessment{Risk: schema.RiskHigh, Confidence: 0.8}
	if err := w.WriteTriage(a); err != nil {
		t.Fatalf("WriteTriage: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "triage.json"))
	if err != nil {
		t.Fatalf("read triage.json: %v", err)
	}
	if data[0] != '{' {
		t.Fatalf("triage.json = %q, want a JSON object", data)
	}
	if len(data) < 2 || data[1] != '\n' {
		t.Errorf("triage.json is not indented (want a newline after '{'): %q", data)
	}
}

func TestWriteRosterRoundTrips(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	r := &roster.Roster{Members: []roster.Member{{ID: "security", Kind: "agent", Evaluated: true}}, TotalTokens: 12000}
	if err := w.WriteRoster(r); err != nil {
		t.Fatalf("WriteRoster: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "roster.json"))
	if err != nil {
		t.Fatalf("read roster.json: %v", err)
	}
	if !contains(string(data), `"id": "security"`) {
		t.Errorf("roster.json = %s, want the member id present", data)
	}
}

func TestWriteFindingsFinalFiltersByAcceptedDisposition(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	findings := []schema.Finding{
		{Envelope: schema.Envelope{ID: "f-0001", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
		{Envelope: schema.Envelope{ID: "f-0002", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
		{Envelope: schema.Envelope{ID: "f-0003", Verification: schema.Verification{Disposition: schema.DispositionMerged}}},
	}
	if err := w.WriteFindingsFinal(findings); err != nil {
		t.Fatalf("WriteFindingsFinal: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "findings.final.json"))
	if err != nil {
		t.Fatalf("read findings.final.json: %v", err)
	}
	if !contains(string(data), "f-0001") {
		t.Errorf("findings.final.json missing the accepted finding: %s", data)
	}
	if contains(string(data), "f-0002") || contains(string(data), "f-0003") {
		t.Errorf("findings.final.json includes a non-accepted finding: %s", data)
	}
}

func TestWriteFindingsRawIncludesEveryDisposition(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	findings := []schema.Finding{
		{Envelope: schema.Envelope{ID: "f-0001", Verification: schema.Verification{Disposition: schema.DispositionAccepted}}},
		{Envelope: schema.Envelope{ID: "f-0002", Verification: schema.Verification{Disposition: schema.DispositionDropped}}},
	}
	if err := w.WriteFindingsRaw(findings); err != nil {
		t.Fatalf("WriteFindingsRaw: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "findings.raw.json"))
	if err != nil {
		t.Fatalf("read findings.raw.json: %v", err)
	}
	if !contains(string(data), "f-0001") || !contains(string(data), "f-0002") {
		t.Errorf("findings.raw.json missing a finding: %s", data)
	}
}

func TestWriteEmptySliceProducesEmptyArrayNotNull(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if err := w.WriteFindingsRaw(nil); err != nil {
		t.Fatalf("WriteFindingsRaw: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "findings.raw.json"))
	if err != nil {
		t.Fatalf("read findings.raw.json: %v", err)
	}
	got := trimNewline(string(data))
	if got != "[]" {
		t.Errorf("findings.raw.json = %q, want [] not null for a nil slice", got)
	}
}

func TestWriteVerdicts(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	verdicts := []verify.Verdict{{Lens: "groundedness", Result: "pass", Checked: "model", Reason: "supported"}}
	if err := w.WriteVerdicts(verdicts); err != nil {
		t.Fatalf("WriteVerdicts: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "verdicts.json"))
	if err != nil {
		t.Fatalf("read verdicts.json: %v", err)
	}
	if !contains(string(data), "groundedness") {
		t.Errorf("verdicts.json = %s, want the lens name present", data)
	}
}

func TestWriteBudgetMapKeysSorted(t *testing.T) {
	w, err := New(t.TempDir())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	b := &roster.Budget{
		Allocated: map[string]int{"zeta": 100, "alpha": 200},
		Consumed:  map[string]int{"zeta": 50, "alpha": 75},
		Usage:     roster.TokenUsage{Prompt: 900, CachedPrompt: 300, Completion: 225, Total: 1125},
	}
	if err := w.WriteBudget(b); err != nil {
		t.Fatalf("WriteBudget: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(w.Dir(), "budget.json"))
	if err != nil {
		t.Fatalf("read budget.json: %v", err)
	}
	alphaIdx := indexOf(string(data), "alpha")
	zetaIdx := indexOf(string(data), "zeta")
	if alphaIdx < 0 || zetaIdx < 0 || alphaIdx > zetaIdx {
		t.Errorf("budget.json map keys not sorted: %s", data)
	}
	for _, field := range []string{`"prompt": 900`, `"cached_prompt": 300`, `"completion": 225`, `"total": 1125`} {
		if !contains(string(data), field) {
			t.Errorf("budget.json = %s, want usage field %s", data, field)
		}
	}
}

func contains(s, sub string) bool { return indexOf(s, sub) >= 0 }

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
