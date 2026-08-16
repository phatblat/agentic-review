package runner

import (
	"context"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/goldentest"
)

// msFieldRE zeroes the summary marker's URL-encoded history "ms" field —
// wall-clock elapsed milliseconds, the only non-deterministic byte in an
// otherwise fully reproducible replay run.
var msFieldRE = regexp.MustCompile(`ms%22%3A\d+%7D`)

func normalizeMS(s string) string {
	return msFieldRE.ReplaceAllString(s, "ms%22%3A0%7D")
}

// TestReplay is Verification item 5, the primary new-behavior proof:
// fixtures/replay/security-token-expiry/ drives the whole pipeline
// through gh.Fake and infer.Replayer (no live model calls, no
// AGENTIC_REVIEW_API_KEY needed) and must produce exactly one batched
// CreateReview call carrying one comment on src/auth/token.rs line 84,
// plus one CreateIssueComment whose first line parses as a kind=summary
// marker with status=findings.
func TestReplay(t *testing.T) {
	root := "../../fixtures/replay/security-token-expiry"
	fake, err := gh.LoadFake(root)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload, err := os.ReadFile(root + "/event.json")
	if err != nil {
		t.Fatalf("read event.json: %v", err)
	}

	outDir := t.TempDir()
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: nil, ConfigRoot: root,
		RunID: 17283, RunURL: "https://github.com/acme/demo/actions/runs/17283",
		OutDir: outDir, ReplayDir: root + "/recordings",
	})
	if exit != 2 {
		t.Fatalf("Review exit = %d, want 2 (an error-severity finding survives under the default fail_on: nit)", exit)
	}

	var reviewCalls, issueComments int
	var reviewBody, summaryBody string
	for _, p := range fake.Posted {
		switch p.Method {
		case "CreateReview":
			reviewCalls++
			if len(p.Comments) != 1 {
				t.Fatalf("CreateReview carried %d comments, want exactly 1", len(p.Comments))
			}
			c := p.Comments[0]
			if c.Path != "src/auth/token.rs" || c.Line != 84 {
				t.Errorf("comment anchor = %s:%d, want src/auth/token.rs:84", c.Path, c.Line)
			}
			reviewBody = c.Body
		case "CreateIssueComment":
			issueComments++
			summaryBody = p.Body
		default:
			t.Errorf("unexpected posted call: %+v", p)
		}
	}
	if reviewCalls != 1 {
		t.Fatalf("CreateReview calls = %d, want exactly 1 (batched)", reviewCalls)
	}
	if issueComments != 1 {
		t.Fatalf("CreateIssueComment calls = %d, want exactly 1", issueComments)
	}
	// fp's sha256: prefix is url.QueryEscape'd like every marker field
	// (spec item 37, already covered by marker_test.go's
	// TestRenderFindingFixedKeyOrder), so the colon renders as %3A.
	if !strings.HasPrefix(reviewBody, "<!-- agentic-review/1 kind=finding fp=sha256%3A") {
		t.Errorf("comment body = %q, want it to start with the finding marker", reviewBody)
	}
	if !strings.Contains(reviewBody, "🚨 **Token expiry check removed**") {
		t.Errorf("comment body = %q, want the emoji+title line", reviewBody)
	}
	if !strings.HasPrefix(summaryBody, "<!-- agentic-review/1 kind=summary") || !strings.Contains(summaryBody, "status=findings") {
		t.Errorf("summary body = %q, want a kind=summary marker with status=findings on its first line", summaryBody)
	}

	postedJSON, err := json.MarshalIndent(fake.Posted, "", "  ")
	if err != nil {
		t.Fatalf("marshal Posted: %v", err)
	}
	var posted any
	if err := json.Unmarshal([]byte(normalizeMS(string(postedJSON))), &posted); err != nil {
		t.Fatalf("unmarshal normalized Posted: %v", err)
	}
	goldentest.JSON(t, root+"/want-comments.json", posted)

	final, err := os.ReadFile(outDir + "/findings.final.json")
	if err != nil {
		t.Fatalf("read findings.final.json: %v", err)
	}
	var findings any
	if err := json.Unmarshal(final, &findings); err != nil {
		t.Fatalf("unmarshal findings.final.json: %v", err)
	}
	goldentest.JSON(t, root+"/want-findings.final.json", findings)
}
