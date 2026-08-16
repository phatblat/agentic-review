// Package artifact writes one run's artifacts (spec §13.3): triage.json,
// roster.json, findings.raw.json, verdicts.json, findings.final.json, and
// budget.json, plus the recordings/ directory name convention when
// --record is set.
package artifact

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/verify"
)

// RecordingsDirName is the subdirectory infer.NewRecorder writes to
// within a Writer's directory when --record is set.
const RecordingsDirName = "recordings"

// Writer writes one run's artifacts to a resolved output directory.
type Writer struct {
	dir string
}

// New resolves the output directory and creates it. out, when non-empty,
// overrides both env-derived defaults. Otherwise: RUNNER_TEMP/agentic-review
// when RUNNER_TEMP is set, else ./.agentic-review/run/.
func New(out string) (*Writer, error) {
	dir := out
	if dir == "" {
		if rt := os.Getenv("RUNNER_TEMP"); rt != "" {
			dir = filepath.Join(rt, "agentic-review")
		} else {
			dir = filepath.Join(".agentic-review", "run")
		}
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("artifact: create %s: %w", dir, err)
	}
	return &Writer{dir: dir}, nil
}

// Dir returns the resolved output directory.
func (w *Writer) Dir() string { return w.dir }

// RecordingsDir returns the recordings/ subdirectory path, for wiring
// into TeamDeps.RecordDir / verify.Env.RecordDir when --record is set.
func (w *Writer) RecordingsDir() string { return filepath.Join(w.dir, RecordingsDirName) }

// WriteTriage writes triage.json. a may be nil when triage failed
// entirely and the fallback roster ran; callers pass nil to record that.
func (w *Writer) WriteTriage(a *schema.Assessment) error { return w.writeJSON("triage.json", a) }

// WriteRoster writes roster.json.
func (w *Writer) WriteRoster(r *roster.Roster) error { return w.writeJSON("roster.json", r) }

// WriteFindingsRaw writes findings.raw.json: every finding produced this
// run, including dropped/downgraded/merged ones, for the audit trail.
func (w *Writer) WriteFindingsRaw(findings []schema.Finding) error {
	return w.writeJSON("findings.raw.json", nonNil(findings))
}

// WriteVerdicts writes verdicts.json: every verdict every lens recorded
// this run.
func (w *Writer) WriteVerdicts(verdicts []verify.Verdict) error {
	return w.writeJSON("verdicts.json", nonNil(verdicts))
}

// WriteFindingsFinal writes findings.final.json: findings filtered to
// disposition == accepted, the set actually posted to GitHub.
func (w *Writer) WriteFindingsFinal(findings []schema.Finding) error {
	final := make([]schema.Finding, 0, len(findings))
	for _, f := range findings {
		if f.Envelope.Verification.Disposition == schema.DispositionAccepted {
			final = append(final, f)
		}
	}
	return w.writeJSON("findings.final.json", final)
}

// WriteBudget writes budget.json.
func (w *Writer) WriteBudget(b *roster.Budget) error { return w.writeJSON("budget.json", b) }

// writeJSON writes v to name inside w.dir with json.Encoder +
// SetIndent("", "  "); encoding/json already sorts map keys, so output is
// byte-exact for golden comparison.
func (w *Writer) writeJSON(name string, v any) error {
	path := filepath.Join(w.dir, name)
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("artifact: create %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("artifact: encode %s: %w", path, err)
	}
	return nil
}

// nonNil returns s itself when non-nil, else an empty slice — a nil slice
// encodes to JSON "null", but an empty run's artifact should read "[]".
func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}
