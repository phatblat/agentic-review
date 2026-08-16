package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/render"
)

// reviewFixture builds a gh.Fake fixture directory (the simulated GitHub
// API state) and a separate .github/agentic-review/ config directory
// (what localconfig.Load reads from disk, mirroring a real checkout).
func reviewFixture(t *testing.T, prJSON, filesJSON, configYAML string) (fakeDir, configRoot string) {
	t.Helper()
	fakeDir = t.TempDir()
	writeReviewFile(t, filepath.Join(fakeDir, "pr.json"), prJSON)
	if filesJSON != "" {
		writeReviewFile(t, filepath.Join(fakeDir, "files.json"), filesJSON)
	}

	configRoot = t.TempDir()
	writeReviewFile(t, filepath.Join(configRoot, ".github/agentic-review/config.yaml"), configYAML)
	return fakeDir, configRoot
}

func writeReviewFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const reviewModelsConfig = "version: 1\nmodels:\n" +
	"  triage: {endpoint: \"http://spark/v1\", model: \"triage-model\"}\n" +
	"  review: {endpoint: \"http://spark/v1\", model: \"review-model\"}\n" +
	"  verify: {endpoint: \"http://spark/v1\", model: \"verify-model\"}\n"

// TestApplyOverrides covers spec item 46's action.yml `endpoint` and
// `fail_on` inputs: endpoint only fills a blank models[] binding
// (config.yaml's own value always wins), fail_on replaces
// cfg.Review.Gate.FailOn outright whenever set.
func TestApplyOverrides(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{
		"triage": {Endpoint: "", Model: "triage-model"},
		"review": {Endpoint: "http://configured/v1", Model: "review-model"},
	}
	cfg.Review.Gate.FailOn = "nit"

	applyOverrides(cfg, ReviewDeps{EndpointOverride: "http://override/v1", FailOnOverride: "warning"})

	if got := cfg.Models["triage"].Endpoint; got != "http://override/v1" {
		t.Errorf("triage endpoint = %q, want the override to fill the blank binding", got)
	}
	if got := cfg.Models["review"].Endpoint; got != "http://configured/v1" {
		t.Errorf("review endpoint = %q, want config.yaml's own value preserved, not overridden", got)
	}
	if cfg.Review.Gate.FailOn != "warning" {
		t.Errorf("Gate.FailOn = %q, want the override value", cfg.Review.Gate.FailOn)
	}
}

// TestApplyOverridesNoOpWhenUnset confirms empty overrides leave cfg
// untouched — the zero-value ReviewDeps used by every other test in this
// file must not accidentally mutate config.yaml's own settings.
func TestApplyOverridesNoOpWhenUnset(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{"triage": {Endpoint: "", Model: "triage-model"}}
	cfg.Review.Gate.FailOn = "nit"

	applyOverrides(cfg, ReviewDeps{})

	if got := cfg.Models["triage"].Endpoint; got != "" {
		t.Errorf("triage endpoint = %q, want it left blank with no override set", got)
	}
	if cfg.Review.Gate.FailOn != "nit" {
		t.Errorf("Gate.FailOn = %q, want it untouched with no override set", cfg.Review.Gate.FailOn)
	}
}

func TestReviewTierZeroSkipPostsSkipSummaryAndExitsZero(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","author_association":"OWNER"}`
	filesJSON := `[{"path":"README.md","status":"modified","additions":1,"deletions":1,"patch":"@@ -1,1 +1,1 @@\n-old\n+new\n"}]`
	fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload := []byte(`{"action":"opened","number":7,"pull_request":{"draft":false},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: &keyedClient{}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://github.com/acme/demo/actions/runs/1", OutDir: t.TempDir(),
	})
	if exit != 0 {
		t.Fatalf("Review exit = %d, want 0", exit)
	}

	var summaryPosted bool
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" {
			summaryPosted = true
			if !contains(p.Body, "skipped agentic review") {
				t.Errorf("summary body = %q, want the skip variant", p.Body)
			}
		}
		if p.Method == "CreateReview" || p.Method == "CreateFileComment" {
			t.Errorf("a docs-only skip posted a finding comment: %+v", p)
		}
	}
	if !summaryPosted {
		t.Errorf("Posted = %+v, want a skip summary comment", fake.Posted)
	}
}

// noCallClient fails the test if Complete is ever invoked — Verification
// item 6's proof that a tier-0 skip makes zero model calls.
type noCallClient struct{ t *testing.T }

func (c noCallClient) Complete(context.Context, string, *infer.Request) (*infer.Response, error) {
	c.t.Fatal("infer.Client.Complete was called on a tier-0 skip; no model call should ever happen")
	return nil, nil
}

// TestSkipDepsOnly is Verification item 6: a deps-only PR (here, a
// Cargo.lock-only change — a known lockfile, classified by filename
// alone, no content inspection) makes zero infer.Client calls, posts one
// summary reading "✅ skipped agentic review: deps-only change" with a
// "🔢 0 tokens" footer, and exits 0.
func TestSkipDepsOnly(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","author_association":"OWNER"}`
	filesJSON := `[{"path":"Cargo.lock","status":"modified","additions":3,"deletions":3,"patch":"@@ -1,3 +1,3 @@\n-a\n+b\n"}]`
	fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload := []byte(`{"action":"opened","number":7,"pull_request":{"draft":false},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: noCallClient{t: t}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
	})
	if exit != 0 {
		t.Fatalf("Review exit = %d, want 0", exit)
	}

	var summaryBody string
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" {
			summaryBody = p.Body
		}
		if p.Method == "CreateReview" || p.Method == "CreateFileComment" {
			t.Errorf("a deps-only skip posted a finding comment: %+v", p)
		}
	}
	if !contains(summaryBody, "✅ skipped agentic review: deps-only change") {
		t.Errorf("summary = %q, want the deps-only skip variant", summaryBody)
	}
	if !contains(summaryBody, "🔢 0 tokens") {
		t.Errorf("summary = %q, want a 0 tokens footer", summaryBody)
	}
}

func TestReviewDraftPROpenedSkipsWithoutPosting(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","draft":true}`
	fakeDir, configRoot := reviewFixture(t, prJSON, "", reviewModelsConfig)
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload := []byte(`{"action":"opened","number":7,"pull_request":{"draft":true},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: &keyedClient{}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
	})
	if exit != 0 {
		t.Fatalf("Review exit = %d, want 0", exit)
	}
	if len(fake.Posted) != 0 {
		t.Errorf("Posted = %+v, want nothing posted for a no-op draft skip", fake.Posted)
	}
}

func TestReviewMalformedConfigFailsInfraWithErrorSummary(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha"}`
	fakeDir, configRoot := reviewFixture(t, prJSON, "[]", "version: 1\nreview: [not, a, map]\n")
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload := []byte(`{"action":"opened","number":7,"pull_request":{"draft":false},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: &keyedClient{}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
	})
	if exit != 1 {
		t.Fatalf("Review exit = %d, want 1 (malformed config)", exit)
	}
	var errorPosted bool
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" && contains(p.Body, "config load failed") {
			errorPosted = true
		}
	}
	if !errorPosted {
		t.Errorf("Posted = %+v, want an error summary naming the failed stage", fake.Posted)
	}
}

func TestReviewIssueCommentPermissionDeniedPostsAckAndExitsZero(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha"}`
	fakeDir, configRoot := reviewFixture(t, prJSON, "[]", reviewModelsConfig)
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	// PermissionLevel defaults to "none" for any user not seeded in
	// permissions.json — exactly the denial path under test.

	payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":"/agentic-review","user":{"login":"outsider"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
		Port: fake, Client: &keyedClient{}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
	})
	if exit != 0 {
		t.Fatalf("Review exit = %d, want 0", exit)
	}
	var ackPosted bool
	for _, p := range fake.Posted {
		if p.Method == "CreateIssueComment" && contains(p.Body, "requires write access") && contains(p.Body, "outsider") {
			ackPosted = true
		}
		if p.Method == "ReactIssueComment" {
			t.Errorf("reacted to a permission-denied summons: %+v", p)
		}
	}
	if !ackPosted {
		t.Errorf("Posted = %+v, want the permission-denial ack comment", fake.Posted)
	}
}

func TestReviewIssueCommentAuthorizedReactsWithEyes(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","author_association":"OWNER"}`
	filesJSON := `[{"path":"README.md","status":"modified","additions":1,"deletions":1,"patch":"@@ -1,1 +1,1 @@\n-old\n+new\n"}]`
	fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
	writeReviewFile(t, filepath.Join(fakeDir, "permissions.json"), `{"maintainer":"write"}`)
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":"/agentic-review","user":{"login":"maintainer"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
		Port: fake, Client: &keyedClient{}, ConfigRoot: configRoot,
		RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
	})
	if exit != 0 {
		t.Fatalf("Review exit = %d, want 0 (docs-only change tier-0 skips after the summons is authorized)", exit)
	}
	var reacted bool
	for _, p := range fake.Posted {
		if p.Method == "ReactIssueComment" && p.ID == 555 && p.Content == "eyes" {
			reacted = true
		}
	}
	if !reacted {
		t.Errorf("Posted = %+v, want an eyes reaction on the authorized summons comment", fake.Posted)
	}
}

func TestReviewFullPipelineHappyPath(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","author_association":"OWNER"}`
	filesJSON := `[{"path":"src/main.go","status":"modified","additions":1,"deletions":1,` +
		`"patch":"@@ -1,3 +1,3 @@\n line1\n-old\n+new\n line3\n"}]`
	fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
	writeReviewFile(t, filepath.Join(fakeDir, "head", "src", "main.go"), "line1\nnew\nline3\n")
	fake, err := gh.LoadFake(fakeDir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}

	triageJSON := `{"risk":"low","complexity":"trivial","domains":[],"summary":"s","rationale":"r","suggested_personas":[],"confidence":0.7}`
	findingsJSON := `{"findings":[{"category":"correctness","severity":"warning","title":"off-by-one","claim":"the loop bound is wrong",` +
		`"anchor":{"kind":"line","path":"src/main.go","start_line":2,"end_line":2,"ref":"head"},` +
		`"evidence":[{"path":"src/main.go","start_line":2,"end_line":2,"source":"new"}],"confidence":0.6}],"escalate":[]}`
	verdictsJSON := `{"verdicts":[]}`

	cl := &keyedClient{responses: map[string]string{
		"triage-model": triageJSON, "review-model": findingsJSON, "verify-model": verdictsJSON,
	}}

	outDir := t.TempDir()
	payload := []byte(`{"action":"opened","number":7,"pull_request":{"draft":false},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
	exit := Review(context.Background(), "pull_request", payload, ReviewDeps{
		Port: fake, Client: cl, ConfigRoot: configRoot,
		RunID: 42, RunURL: "https://github.com/acme/demo/actions/runs/42", OutDir: outDir,
	})
	// gate.Defaults() fail_on: nit means any surviving finding fails the
	// gate — the logic persona's warning-severity finding should survive
	// mechanical validation (its evidence matches head content exactly)
	// and every lens leaving it unjudged, so exit must be 2.
	if exit != 2 {
		t.Fatalf("Review exit = %d, want 2 (a surviving finding under the default fail_on: nit)", exit)
	}

	var reviewPosted, summaryPosted bool
	var summaryBody string
	for _, p := range fake.Posted {
		if p.Method == "CreateReview" {
			reviewPosted = true
			if len(p.Comments) != 1 || !contains(p.Comments[0].Body, "off-by-one") {
				t.Errorf("CreateReview comments = %+v, want the logic finding", p.Comments)
			}
		}
		if p.Method == "CreateIssueComment" {
			summaryPosted = true
			summaryBody = p.Body
		}
	}
	if !reviewPosted {
		t.Errorf("Posted = %+v, want a CreateReview call for the line-anchored finding", fake.Posted)
	}
	if !summaryPosted {
		t.Fatalf("Posted = %+v, want a summary comment", fake.Posted)
	}
	if !contains(summaryBody, "logic") {
		t.Errorf("summary = %q, want the logic persona credited in the team footer", summaryBody)
	}

	for _, name := range []string{"triage.json", "roster.json", "findings.raw.json", "verdicts.json", "findings.final.json", "budget.json"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("artifact %s was not written: %v", name, err)
		}
	}
	rosterData, err := os.ReadFile(filepath.Join(outDir, "roster.json"))
	if err != nil {
		t.Fatalf("read roster.json: %v", err)
	}
	if !contains(string(rosterData), `"id": "logic"`) {
		t.Errorf("roster.json = %s, want logic present (activation.always: true)", rosterData)
	}
}

// TestSummons is Verification item 6b's four summons-path scenarios.
func TestSummons(t *testing.T) {
	prJSON := `{"number":7,"base_sha":"basesha","head_sha":"headsha","author_association":"OWNER"}`
	filesJSON := `[{"path":"src/auth/handler.go","status":"modified","additions":1,"deletions":1,` +
		`"patch":"@@ -1,1 +1,1 @@\n-old\n+new\n"}]`

	t.Run("read permission denied", func(t *testing.T) {
		fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
		writeReviewFile(t, filepath.Join(fakeDir, "permissions.json"), `{"outsider":"read"}`)
		fake, err := gh.LoadFake(fakeDir)
		if err != nil {
			t.Fatalf("LoadFake: %v", err)
		}

		payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":"/agentic-review security","user":{"login":"outsider"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
		exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
			Port: fake, Client: noCallClient{t: t}, ConfigRoot: configRoot,
			RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
		})
		if exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}

		var issueComments, reacts int
		var ackBody string
		for _, p := range fake.Posted {
			switch p.Method {
			case "CreateIssueComment":
				issueComments++
				ackBody = p.Body
			case "ReactIssueComment":
				reacts++
			}
		}
		if issueComments != 1 {
			t.Fatalf("CreateIssueComment calls = %d, want exactly 1", issueComments)
		}
		if reacts != 0 {
			t.Errorf("ReactIssueComment calls = %d, want 0", reacts)
		}
		m, ok := render.Parse(ackBody)
		if !ok || m.Kind != "ack" {
			t.Errorf("ack body = %q, want its first line to parse as kind=ack", ackBody)
		}
	})

	t.Run("write permission narrows team to scope", func(t *testing.T) {
		fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
		writeReviewFile(t, filepath.Join(fakeDir, "head", "src", "auth", "handler.go"), "line1\nnew\nline3\n")
		writeReviewFile(t, filepath.Join(fakeDir, "permissions.json"), `{"maintainer":"write"}`)
		fake, err := gh.LoadFake(fakeDir)
		if err != nil {
			t.Fatalf("LoadFake: %v", err)
		}

		triageJSON := `{"risk":"low","complexity":"trivial","domains":[],"summary":"s","rationale":"r","suggested_personas":[],"confidence":0.7}`
		findingsJSON := `{"findings":[],"escalate":[]}`
		cl := &keyedClient{responses: map[string]string{"triage-model": triageJSON, "review-model": findingsJSON}}

		outDir := t.TempDir()
		payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":"/agentic-review security","user":{"login":"maintainer"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
		exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
			Port: fake, Client: cl, ConfigRoot: configRoot,
			RunID: 1, RunURL: "https://x", OutDir: outDir,
		})
		if exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}

		var reacted bool
		for _, p := range fake.Posted {
			if p.Method == "ReactIssueComment" && p.ID == 555 && p.Content == "eyes" {
				reacted = true
			}
		}
		if !reacted {
			t.Errorf("Posted = %+v, want a ReactIssueComment(555, eyes) call", fake.Posted)
		}

		rosterData, err := os.ReadFile(filepath.Join(outDir, "roster.json"))
		if err != nil {
			t.Fatalf("read roster.json: %v", err)
		}
		if !contains(string(rosterData), `"id": "security"`) {
			t.Errorf("roster.json = %s, want security present", rosterData)
		}
		if contains(string(rosterData), `"id": "logic"`) {
			t.Errorf("roster.json = %s, want logic excluded (scope narrowed to security)", rosterData)
		}
	})

	t.Run("bot author exits zero with no writes", func(t *testing.T) {
		fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
		fake, err := gh.LoadFake(fakeDir)
		if err != nil {
			t.Fatalf("LoadFake: %v", err)
		}

		payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":"/agentic-review","user":{"login":"a-bot","type":"Bot"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
		exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
			Port: fake, Client: noCallClient{t: t}, ConfigRoot: configRoot,
			RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
		})
		if exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		if len(fake.Posted) != 0 {
			t.Errorf("Posted = %+v, want zero API writes for a bot-authored comment", fake.Posted)
		}
	})

	t.Run("own marker echo exits zero with no writes", func(t *testing.T) {
		fakeDir, configRoot := reviewFixture(t, prJSON, filesJSON, reviewModelsConfig)
		fake, err := gh.LoadFake(fakeDir)
		if err != nil {
			t.Fatalf("LoadFake: %v", err)
		}

		body := "<!-- agentic-review/1 kind=summary run=1 status=findings history= -->\n> quoted content mentioning /agentic-review"
		payload := []byte(`{"action":"created","issue":{"number":7,"pull_request":{}},"comment":{"id":555,"body":` +
			jsonQuote(body) + `,"user":{"login":"maintainer"}},"repository":{"name":"demo","owner":{"login":"acme"}}}`)
		exit := Review(context.Background(), "issue_comment", payload, ReviewDeps{
			Port: fake, Client: noCallClient{t: t}, ConfigRoot: configRoot,
			RunID: 1, RunURL: "https://x", OutDir: t.TempDir(),
		})
		if exit != 0 {
			t.Fatalf("exit = %d, want 0", exit)
		}
		if len(fake.Posted) != 0 {
			t.Errorf("Posted = %+v, want zero API writes for the bot's own marker echoed back", fake.Posted)
		}
	})
}

// jsonQuote renders s as a JSON string literal for hand-built payloads.
func jsonQuote(s string) string {
	data, _ := json.Marshal(s)
	return string(data)
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
