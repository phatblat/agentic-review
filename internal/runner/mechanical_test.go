package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/diffscan"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func mechTestStore(t *testing.T, headContent map[string]string) *gh.ContentStore {
	t.Helper()
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pr.json"), `{"number":1,"head_sha":"headsha","base_sha":"basesha"}`)
	for path, content := range headContent {
		mustWrite(t, filepath.Join(dir, "head", path), content)
	}
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	return gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"})
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func agentFinding(path string, startLine, endLine int, source string) schema.Finding {
	return schema.Finding{
		Payload: schema.Payload{
			Category: "correctness", Severity: "warning", Title: "t", Claim: "c",
			Anchor:   schema.Anchor{Kind: schema.AnchorLine, Path: path, StartLine: startLine, EndLine: endLine, Ref: schema.RefHead},
			Evidence: []schema.Evidence{{Path: path, StartLine: startLine, EndLine: endLine, Source: source}},
		},
		Envelope: schema.Envelope{PersonaKind: string(persona.KindAgent)},
	}
}

func TestMechanicalValidateEvidenceByteMatchSurvives(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2")

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if len(out) != 1 || out[0].Envelope.Verification.Disposition == schema.DispositionDropped {
		t.Fatalf("out = %+v, want the byte-matching finding to survive", out)
	}
}

// config-guard's model pass emits findings/v1 evidence under a
// deterministic persona; it gets the same byte-match as an agent's, while
// the persona's code-computed facts (no evidence) are left alone.
func TestMechanicalValidateChecksEvidenceOfModelAuthoredDeterministicFindings(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	judged := agentFinding("f.go", 2, 2, "invented quote")
	judged.Envelope.PersonaKind = string(persona.KindDeterministic)
	judged.Envelope.ModelAuthored = true
	fact := schema.Finding{
		Payload:  schema.Payload{Category: "config", Severity: "blocker", Title: "t", Claim: "c", Anchor: schema.Anchor{Kind: schema.AnchorFile, Path: "f.go"}},
		Envelope: schema.Envelope{ID: "fact", PersonaKind: string(persona.KindDeterministic)},
	}
	judged.Envelope.ID = "judged"

	out := mechanicalValidate(context.Background(), []schema.Finding{judged, fact}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)

	got := map[string]schema.Disposition{}
	for _, f := range out {
		got[f.Envelope.ID] = f.Envelope.Verification.Disposition
	}
	if got["judged"] != schema.DispositionDropped {
		t.Errorf("judged disposition = %q, want dropped: its evidence does not match the file", got["judged"])
	}
	if got["fact"] == schema.DispositionDropped {
		t.Errorf("fact was dropped, want a no-evidence deterministic fact left alone")
	}
}

// config-guard's model pass is shown both sides of each config file and is
// asked for a single anchor.ref, but its findings naturally quote removed
// and added text together ("X changed from A to B") or quote a deleted file.
// Each evidence entry of a ModelAuthored finding is grounded against the
// anchor's side first, then the other; the quote must still exist verbatim
// in one real file.
func TestMechanicalValidateModelAuthoredEvidenceMayComeFromEitherSide(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "pr.json"), `{"number":1,"head_sha":"headsha","base_sha":"basesha"}`)
	mustWrite(t, filepath.Join(dir, "base", "deleted.yaml"), "id: security\nrequired_when: x\n")
	mustWrite(t, filepath.Join(dir, "base", "c.yaml"), "gate:\n  fail_on: warning\n")
	mustWrite(t, filepath.Join(dir, "head", "c.yaml"), "gate:\n  fail_on: error\n")
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	store := gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"})

	finding := func(id string, kind persona.Kind, modelAuthored bool, ref schema.AnchorRef, ev ...schema.Evidence) schema.Finding {
		return schema.Finding{
			Payload: schema.Payload{
				Category: "config", Severity: "warning", Title: "t", Claim: "c",
				Anchor:   schema.Anchor{Kind: schema.AnchorFile, Path: ev[0].Path, Ref: ref},
				Evidence: ev,
			},
			Envelope: schema.Envelope{ID: id, PersonaKind: string(kind), ModelAuthored: modelAuthored},
		}
	}
	oldQuote := schema.Evidence{Path: "c.yaml", StartLine: 2, EndLine: 2, Source: "fail_on: warning"}
	newQuote := schema.Evidence{Path: "c.yaml", StartLine: 2, EndLine: 2, Source: "fail_on: error"}
	deleted := schema.Evidence{Path: "deleted.yaml", StartLine: 2, EndLine: 2, Source: "required_when: x"}
	invented := schema.Evidence{Path: "c.yaml", StartLine: 2, EndLine: 2, Source: "fail_on: never"}

	det := persona.KindDeterministic
	out := mechanicalValidate(context.Background(), []schema.Finding{
		finding("deleted-default-ref", det, true, "", deleted),
		finding("two-sided-head-anchor", det, true, schema.RefHead, oldQuote, newQuote),
		finding("two-sided-base-anchor", det, true, schema.RefBase, newQuote, oldQuote),
		finding("in-neither-side", det, true, schema.RefBase, oldQuote, invented),
		finding("agent-no-fallback", persona.KindAgent, false, "", deleted),
	}, store, nil, map[string]bool{"c.yaml": true, "deleted.yaml": true}, "headsha", "basesha", 50)

	got := map[string]schema.Disposition{}
	for _, f := range out {
		got[f.Envelope.ID] = f.Envelope.Verification.Disposition
	}
	want := map[string]bool{ // true = dropped
		"deleted-default-ref":   false,
		"two-sided-head-anchor": false,
		"two-sided-base-anchor": false,
		"in-neither-side":       true,
		"agent-no-fallback":     true, // agents get no second side
	}
	for id, dropped := range want {
		if isDropped := got[id] == schema.DispositionDropped; isDropped != dropped {
			t.Errorf("%s: dropped = %v, want %v (disposition %q)", id, isDropped, dropped, got[id])
		}
	}
}

func TestMechanicalValidateEvidenceMismatchDrops(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "not what is actually there")

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if len(out) != 1 {
		t.Fatalf("out = %+v, want the mismatched finding still present but dropped", out)
	}
	if out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("disposition = %q, want dropped", out[0].Envelope.Verification.Disposition)
	}
	found := false
	for _, v := range out[0].Envelope.Verification.Verdicts {
		if v.Lens == "groundedness" && v.Result == "fail" {
			found = true
		}
	}
	if !found {
		t.Errorf("verdicts = %+v, want a failed groundedness verdict", out[0].Envelope.Verification.Verdicts)
	}
}

// The filtered-findings section (spec §8.3) renders "lens: reason" from
// these verdicts, so a mechanical drop with no reason renders as a bare
// colon and tells the operator nothing about why the finding vanished.
func TestMechanicalValidateUngroundedEvidenceExplainsWhy(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})

	cases := map[string]struct {
		finding schema.Finding
		want    string
	}{
		"quote appears nowhere in the file": {
			finding: agentFinding("f.go", 2, 2, "not what is actually there"),
			want:    "does not appear anywhere in f.go at head",
		},
		"path not readable": {
			finding: agentFinding("absent.go", 1, 1, "line1"),
			want:    "unreadable at head",
		},
		"empty quote": {
			finding: agentFinding("f.go", 2, 2, "   \n\n"),
			want:    "empty quote",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := mechanicalValidate(context.Background(), []schema.Finding{tc.finding}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
			if len(out) != 1 || out[0].Envelope.Verification.Disposition != schema.DispositionDropped {
				t.Fatalf("out = %+v, want one dropped finding", out)
			}
			verdicts := out[0].Envelope.Verification.Verdicts
			if len(verdicts) != 1 {
				t.Fatalf("verdicts = %+v, want exactly one", verdicts)
			}
			if verdicts[0].Reason == "" {
				t.Fatal("verdict reason is empty; the filtered-findings section would render a bare \"groundedness: \"")
			}
			if !strings.Contains(verdicts[0].Reason, tc.want) {
				t.Errorf("reason = %q, want it to contain %q", verdicts[0].Reason, tc.want)
			}
		})
	}
}

// A model that quotes real code but cites the wrong line, or reflows away
// the indentation, has still produced a checkable finding. Measured on
// Qwen3.6-35B, that describes every finding in a run — so refusing them
// turns a review with real findings into a silent all-clear. The quote
// must exist; the citation gets corrected to where it actually is.
func TestMechanicalValidateCorrectsMisplacedCitation(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "package p\n\nfunc f() {\n\treturn nil\n}\n"})

	cases := map[string]struct {
		finding            schema.Finding
		wantStart, wantEnd int
	}{
		"cited line is wrong": {
			finding:   agentFinding("f.go", 2, 2, "\treturn nil"),
			wantStart: 4, wantEnd: 4,
		},
		"indentation stripped by the model": {
			finding:   agentFinding("f.go", 4, 4, "return nil"),
			wantStart: 4, wantEnd: 4,
		},
		"multi-line quote cited too early": {
			finding:   agentFinding("f.go", 1, 2, "func f() {\nreturn nil\n}"),
			wantStart: 3, wantEnd: 5,
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			out := mechanicalValidate(context.Background(), []schema.Finding{tc.finding}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
			if len(out) != 1 {
				t.Fatalf("out = %+v, want one finding", out)
			}
			if out[0].Envelope.Verification.Disposition == schema.DispositionDropped {
				t.Fatalf("finding was dropped; its quote is real code and belongs in the review: %+v",
					out[0].Envelope.Verification.Verdicts)
			}
			got := out[0].Payload.Evidence[0]
			if got.StartLine != tc.wantStart || got.EndLine != tc.wantEnd {
				t.Errorf("evidence lines = %d-%d, want %d-%d", got.StartLine, got.EndLine, tc.wantStart, tc.wantEnd)
			}
		})
	}
}

// verdicts.json is the audit trail for findings that never reach the
// review, and mechanical drops never pass through a lens — so building it
// from verify.Run's return alone loses exactly the verdicts that explain
// a missing finding.
func TestEnvelopeVerdictsIncludesMechanicalDrops(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	out := mechanicalValidate(context.Background(),
		[]schema.Finding{agentFinding("f.go", 2, 2, "fabricated quote"), agentFinding("f.go", 2, 2, "line2")},
		store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)

	verdicts := envelopeVerdicts(out)
	if len(verdicts) != 1 {
		t.Fatalf("verdicts = %+v, want the one mechanical drop (the matching finding carries none)", verdicts)
	}
	if verdicts[0].Lens != "groundedness" || verdicts[0].Result != "fail" || verdicts[0].Checked != "mechanical" {
		t.Errorf("verdict = %+v, want a failed mechanical groundedness verdict", verdicts[0])
	}
	if verdicts[0].Reason == "" {
		t.Error("verdict reason is empty in verdicts.json")
	}
}

func TestMechanicalValidateTrailingWhitespaceNormalized(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2   \nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2") // no trailing spaces in the model's evidence

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if out[0].Envelope.Verification.Disposition == schema.DispositionDropped {
		t.Errorf("trailing-whitespace-only difference should not drop the finding")
	}
}

func TestMechanicalValidateAnchorOutsideDiffDemotesToFile(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2")
	coverage := map[string]diffscan.Coverage{"f.go": {Right: map[int]bool{99: true}}} // line 2 not covered

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, coverage, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if out[0].Payload.Anchor.Kind != schema.AnchorFile {
		t.Errorf("anchor kind = %q, want demotion to file (path is in the diff)", out[0].Payload.Anchor.Kind)
	}
}

func TestMechanicalValidateAnchorOutsideDiffDemotesToPRWhenPathNotChanged(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2")
	coverage := map[string]diffscan.Coverage{"f.go": {Right: map[int]bool{99: true}}}

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, coverage, map[string]bool{}, "headsha", "basesha", 50)
	if out[0].Payload.Anchor.Kind != schema.AnchorPR {
		t.Errorf("anchor kind = %q, want demotion to pr (path not in diff)", out[0].Payload.Anchor.Kind)
	}
}

func TestMechanicalValidateSuggestedFixOutOfRangeDropped(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2")
	f.Payload.SuggestedFix = &schema.SuggestedFix{StartLine: 50, EndLine: 51, Replacement: "x"}

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if out[0].Payload.SuggestedFix != nil {
		t.Errorf("suggested_fix = %+v, want it dropped for an out-of-range dry run", out[0].Payload.SuggestedFix)
	}
	if len(out) != 1 {
		t.Errorf("out = %+v, want the finding itself kept despite the dropped fix", out)
	}
}

func TestMechanicalValidateSuggestedFixInRangeKept(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	f := agentFinding("f.go", 2, 2, "line2")
	f.Payload.SuggestedFix = &schema.SuggestedFix{StartLine: 2, EndLine: 2, Replacement: "x"}

	out := mechanicalValidate(context.Background(), []schema.Finding{f}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 50)
	if out[0].Payload.SuggestedFix == nil {
		t.Errorf("suggested_fix was dropped, want it kept for an in-range dry run")
	}
}

func TestMechanicalValidateTruncationDropsLowestSeverityFirst(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	nit := agentFinding("f.go", 1, 1, "line1")
	nit.Payload.Severity = "nit"
	blocker := agentFinding("f.go", 2, 2, "line2")
	blocker.Payload.Severity = "blocker"

	// mechanicalValidate always returns every input finding — truncation
	// marks the excess disposition:dropped rather than shrinking the
	// slice, so findings.raw.json can still record why each was cut.
	out := mechanicalValidate(context.Background(), []schema.Finding{nit, blocker}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 1)
	if len(out) != 2 {
		t.Fatalf("out has %d findings, want both retained (one marked dropped)", len(out))
	}
	var nitOut, blockerOut schema.Finding
	for _, f := range out {
		if f.Payload.Severity == "nit" {
			nitOut = f
		} else {
			blockerOut = f
		}
	}
	if nitOut.Envelope.Verification.Disposition != schema.DispositionDropped {
		t.Errorf("nit disposition = %q, want dropped (lowest severity, over the cap)", nitOut.Envelope.Verification.Disposition)
	}
	if blockerOut.Envelope.Verification.Disposition == schema.DispositionDropped {
		t.Errorf("blocker was dropped by truncation, want it exempt")
	}
}

func TestMechanicalValidateTruncationExemptsBlockers(t *testing.T) {
	store := mechTestStore(t, map[string]string{"f.go": "line1\nline2\nline3\n"})
	b1 := agentFinding("f.go", 1, 1, "line1")
	b1.Payload.Severity = "blocker"
	b2 := agentFinding("f.go", 2, 2, "line2")
	b2.Payload.Severity = "blocker"

	out := mechanicalValidate(context.Background(), []schema.Finding{b1, b2}, store, nil, map[string]bool{"f.go": true}, "headsha", "basesha", 1)
	for _, f := range out {
		if f.Envelope.Verification.Disposition == schema.DispositionDropped {
			t.Errorf("blocker %+v was dropped by truncation, want both exempt from the max_findings=1 cap", f)
		}
	}
}
