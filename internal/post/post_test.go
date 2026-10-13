package post

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/render"
	"github.com/phatblat/agentic-review/internal/schema"
)

func testPR() *gh.PullRequest {
	return &gh.PullRequest{Number: 1, HeadSHA: "headsha", BaseSHA: "basesha"}
}

func lineFinding(id, fp, path string, start, end int, severity string) schema.Finding {
	return schema.Finding{
		Payload: schema.Payload{
			Category: "correctness", Severity: severity, Title: "t-" + id, Claim: "claim",
			Anchor: schema.Anchor{Kind: schema.AnchorLine, Path: path, StartLine: start, EndLine: end, Ref: schema.RefHead},
		},
		Envelope: schema.Envelope{
			ID: id, Fingerprint: fp, Persona: "logic",
			Verification: schema.Verification{Disposition: schema.DispositionAccepted},
		},
	}
}

func TestPostLineFindingsBatchedIntoOneReview(t *testing.T) {
	fake := gh.NewFake(testPR())
	findings := []schema.Finding{
		lineFinding("f-0001", "sha256:a", "a.go", 1, 1, "warning"),
		lineFinding("f-0002", "sha256:b", "b.go", 2, 3, "error"),
	}
	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: findings, DiffPaths: map[string]bool{"a.go": true, "b.go": true},
		Caps: map[string]int{"warning": 10, "error": 20},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	var reviewCalls int
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" {
			reviewCalls++
			if len(p.Comments) != 2 {
				t.Errorf("CreateReview comments = %d, want 2 batched together", len(p.Comments))
			}
			if p.Body != "" {
				t.Errorf("CreateReview body = %q, want empty", p.Body)
			}
		}
	}
	if reviewCalls != 1 {
		t.Errorf("CreateReview calls = %d, want exactly 1 (batched)", reviewCalls)
	}
}

// postLineFindings turns an anchor (start/end line + base/head ref) into a
// gh.ReviewComment. This pins that mapping; internal/gh's wire tests pin the
// ReviewComment → REST body half. Together they cover finding → wire.
func TestPostLineFindingsReviewCommentMapping(t *testing.T) {
	tests := []struct {
		name       string
		start, end int
		ref        schema.AnchorRef
		want       gh.ReviewComment
	}{
		{"single-line head", 10, 10, schema.RefHead, gh.ReviewComment{Path: "a.go", Line: 10, Side: "RIGHT"}},
		{"multi-line head", 8, 12, schema.RefHead, gh.ReviewComment{Path: "a.go", Line: 12, StartLine: 8, Side: "RIGHT"}},
		{"single-line base", 4, 4, schema.RefBase, gh.ReviewComment{Path: "a.go", Line: 4, Side: "LEFT"}},
		{"multi-line base", 3, 7, schema.RefBase, gh.ReviewComment{Path: "a.go", Line: 7, StartLine: 3, Side: "LEFT"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fake := gh.NewFake(testPR())
			f := lineFinding("f-0001", "sha256:a", "a.go", tc.start, tc.end, "warning")
			f.Payload.Anchor.Ref = tc.ref
			err := Post(context.Background(), Input{
				Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
				RunID: 1, Findings: []schema.Finding{f}, DiffPaths: map[string]bool{"a.go": true},
				Caps: map[string]int{"warning": 10},
			})
			if err != nil {
				t.Fatalf("Post: %v", err)
			}
			var got []gh.ReviewComment
			for _, p := range fake.Posted {
				if p.Method == "CreateReview" {
					got = append(got, p.Comments...)
				}
			}
			if len(got) != 1 {
				t.Fatalf("posted %d review comments, want 1", len(got))
			}
			got[0].Body = "" // rendered markdown is covered by internal/render
			if diff := cmp.Diff(tc.want, got[0]); diff != "" {
				t.Errorf("ReviewComment mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestPostFileFindingIndividualComment(t *testing.T) {
	fake := gh.NewFake(testPR())
	f := lineFinding("f-0001", "sha256:a", "a.go", 0, 0, "warning")
	f.Payload.Anchor = schema.Anchor{Kind: schema.AnchorFile, Path: "a.go"}

	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: []schema.Finding{f}, DiffPaths: map[string]bool{"a.go": true},
		Caps: map[string]int{"warning": 10},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	found := false
	for _, p := range fake.Posted {
		if p.Method == "CreateFileComment" && p.Path == "a.go" {
			found = true
		}
	}
	if !found {
		t.Errorf("Posted = %+v, want a CreateFileComment for a.go", fake.Posted)
	}
}

func TestPostFileFindingNotInDiffGoesToSummaryOnly(t *testing.T) {
	fake := gh.NewFake(testPR())
	f := lineFinding("f-0001", "sha256:a", "gone.go", 0, 0, "warning")
	f.Payload.Anchor = schema.Anchor{Kind: schema.AnchorFile, Path: "gone.go"}

	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: []schema.Finding{f}, DiffPaths: map[string]bool{}, // gone.go absent from the diff
		Caps: map[string]int{"warning": 10},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	for _, p := range fake.Posted {
		if p.Method == "CreateFileComment" {
			t.Errorf("Posted a CreateFileComment for a path absent from the diff: %+v", p)
		}
	}
	summary := lastSummary(t, fake)
	if !strings.Contains(summary, "t-f-0001") {
		t.Errorf("summary = %q, want the finding's title in the not-on-changed-lines section", summary)
	}
}

func TestPostPRAnchoredGoesToSummary(t *testing.T) {
	fake := gh.NewFake(testPR())
	f := lineFinding("f-0001", "sha256:a", "", 0, 0, "blocker")
	f.Payload.Anchor = schema.Anchor{Kind: schema.AnchorPR}

	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: []schema.Finding{f}, DiffPaths: map[string]bool{}, Caps: map[string]int{},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	summary := lastSummary(t, fake)
	if !strings.Contains(summary, "t-f-0001") {
		t.Errorf("summary = %q, want the pr-anchored finding rendered", summary)
	}
}

func TestPostDedupSkipsAlreadyPostedFingerprint(t *testing.T) {
	fake := gh.NewFake(testPR())
	// Simulate an earlier run's posted comment carrying this fingerprint.
	priorMarker := render.Render(render.Marker{Kind: "finding", Fields: map[string]string{
		"fp": "sha256:a", "run": "1", "seq": "1", "persona": "logic", "sev": "warning",
	}})
	if err := fake.CreateFileComment(context.Background(), gh.Repo{}, 1, "headsha", "a.go", priorMarker+"\nprior body"); err != nil {
		t.Fatalf("seed CreateFileComment: %v", err)
	}
	fake.Posted = nil // clear the seed call so only this run's calls are asserted below

	f := lineFinding("f-0002", "sha256:a", "a.go", 1, 1, "warning") // same fingerprint as the prior run
	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 2, Findings: []schema.Finding{f}, DiffPaths: map[string]bool{"a.go": true},
		Caps: map[string]int{"warning": 10},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" || p.Method == "CreateFileComment" {
			t.Errorf("Posted %+v, want the already-posted fingerprint skipped", p)
		}
	}
}

func TestPostCapsSuppressExcessPerSeverity(t *testing.T) {
	fake := gh.NewFake(testPR())
	var findings []schema.Finding
	for i := 0; i < 5; i++ {
		findings = append(findings, lineFinding(idFor(i), fpFor(i), "a.go", i+1, i+1, "warning"))
	}
	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: findings, DiffPaths: map[string]bool{"a.go": true},
		Caps: map[string]int{"warning": 2},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	var posted int
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" {
			posted = len(p.Comments)
		}
	}
	if posted != 2 {
		t.Errorf("posted %d comments, want exactly the cap of 2", posted)
	}
	summary := lastSummary(t, fake)
	if !strings.Contains(summary, "3 findings suppressed by cap") {
		t.Errorf("summary = %q, want 3 findings suppressed by cap", summary)
	}
}

func TestPostBlockerNeverCapped(t *testing.T) {
	fake := gh.NewFake(testPR())
	var findings []schema.Finding
	for i := 0; i < 5; i++ {
		findings = append(findings, lineFinding(idFor(i), fpFor(i), "a.go", i+1, i+1, "blocker"))
	}
	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: findings, DiffPaths: map[string]bool{"a.go": true},
		Caps: map[string]int{"blocker": 1}, // must be ignored
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	var posted int
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" {
			posted = len(p.Comments)
		}
	}
	if posted != 5 {
		t.Errorf("posted %d comments, want all 5 (blocker is never capped)", posted)
	}
}

func TestPostFilteredFindingsNeverPostedIndividually(t *testing.T) {
	fake := gh.NewFake(testPR())
	dropped := lineFinding("f-0001", "sha256:a", "a.go", 1, 1, "warning")
	dropped.Envelope.Verification.Disposition = schema.DispositionDropped
	dropped.Envelope.Verification.Verdicts = []schema.EnvelopeVerdict{{Lens: "groundedness", Result: "fail", Reason: "unsupported"}}

	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: []schema.Finding{dropped}, DiffPaths: map[string]bool{"a.go": true},
		Caps: map[string]int{"warning": 10},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" || p.Method == "CreateFileComment" {
			t.Errorf("a dropped finding was individually posted: %+v", p)
		}
	}
	summary := lastSummary(t, fake)
	if !strings.Contains(summary, "groundedness") || !strings.Contains(summary, "unsupported") {
		t.Errorf("summary = %q, want the dropped finding's lens/reason in the filtered details", summary)
	}
}

func TestPostSummaryUpsertEditsExisting(t *testing.T) {
	fake := gh.NewFake(testPR())
	marker := render.Render(render.Marker{Kind: "summary", Fields: map[string]string{"run": "1", "status": "clean", "history": ""}})
	id, err := fake.CreateIssueComment(context.Background(), gh.Repo{}, 1, marker+"\n## agentic-review\n\n✅ No findings")
	if err != nil {
		t.Fatalf("seed CreateIssueComment: %v", err)
	}
	fake.Posted = nil

	err = Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 2, Findings: nil, DiffPaths: map[string]bool{}, Caps: map[string]int{},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	editedID := int64(0)
	created := false
	for _, p := range fake.Posted {
		if p.Method == "EditIssueComment" {
			editedID = p.ID
		}
		if p.Method == "CreateIssueComment" {
			created = true
		}
	}
	if editedID != id {
		t.Errorf("edited id = %d, want %d (the existing summary comment)", editedID, id)
	}
	if created {
		t.Errorf("Posted = %+v, want no new summary comment created", fake.Posted)
	}
}

func TestPostSummaryUpsertCreatesWhenNone(t *testing.T) {
	fake := gh.NewFake(testPR())
	err := Post(context.Background(), Input{
		Port: fake, Repo: gh.Repo{Owner: "acme", Name: "demo"}, Number: 1, HeadSHA: "headsha",
		RunID: 1, Findings: nil, DiffPaths: map[string]bool{}, Caps: map[string]int{},
	})
	if err != nil {
		t.Fatalf("Post: %v", err)
	}
	created := false
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" {
			created = true
		}
	}
	if !created {
		t.Errorf("Posted = %+v, want a new summary comment created", fake.Posted)
	}
}

func lastSummary(t *testing.T, fake *gh.Fake) string {
	t.Helper()
	var last string
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" || p.Method == "EditIssueComment" {
			last = p.Body
		}
	}
	if last == "" {
		t.Fatalf("no summary comment was posted: %+v", fake.Posted)
	}
	return last
}

func idFor(i int) string { return "f-000" + string(rune('1'+i)) }
func fpFor(i int) string { return "sha256:" + string(rune('a'+i)) }
