package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func cgWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// cgStore builds a ContentStore whose base/head content for
// .github/agentic-review/config.yaml (or any other path) differs as
// given, so diffConfigYAML/diffPersonaYAML/diffWorkflowPermissions can be
// exercised end to end through ConfigGuard.
func cgStore(t *testing.T, base, head map[string]string) (*gh.ContentStore, string, string) {
	t.Helper()
	dir := t.TempDir()
	cgWrite(t, filepath.Join(dir, "pr.json"), `{"number":1,"base_sha":"basesha","head_sha":"headsha"}`)
	for path, content := range base {
		cgWrite(t, filepath.Join(dir, "base", path), content)
	}
	for path, content := range head {
		cgWrite(t, filepath.Join(dir, "head", path), content)
	}
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	return gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"}), "basesha", "headsha"
}

// reviewConfigClasses classifies every given path as review-config —
// standing in for facts.Assemble's real per-file classification, which
// ConfigGuard consumes rather than re-deriving from the path itself (spec
// §4: review-config is .github/agentic-review/** OR the invoking workflow
// file, and the latter can't be recognised from the path alone).
func reviewConfigClasses(paths ...string) map[string]classes.Class {
	out := make(map[string]classes.Class, len(paths))
	for _, p := range paths {
		out[p] = classes.ClassReviewConfig
	}
	return out
}

// noJudgment is an infer.Client that always errors, so tests can assert on
// the deterministic half alone — ConfigGuard degrades gracefully when the
// model judgment pass is unavailable (spec §12.4's structural half must
// never depend on it).
type noJudgment struct{}

func (noJudgment) Complete(context.Context, string, *infer.Request) (*infer.Response, error) {
	return nil, errors.New("no model available")
}

func payloadTitles(got []schema.Payload) []string {
	var out []string
	for _, p := range got {
		out = append(out, p.Title)
	}
	return out
}

func containsPayloadTitle(got []schema.Payload, want string) bool {
	for _, p := range got {
		if p.Title == want {
			return true
		}
	}
	return false
}

func TestConfigGuardNoConfigFilesChangedIsNoOp(t *testing.T) {
	store, base, head := cgStore(t, nil, nil)
	out, err := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files:       []gh.File{{Path: "src/main.go"}},
		FileClasses: map[string]classes.Class{"src/main.go": classes.ClassSource},
		BaseSHA:     base, HeadSHA: head,
	})
	if err != nil {
		t.Fatalf("ConfigGuard: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("out = %+v, want none: no review-config path changed", out)
	}
}

func TestConfigGuardGateFailOnWeakened(t *testing.T) {
	baseYAML := "version: 1\nreview:\n  gate:\n    fail_on: warning\n"
	headYAML := "version: 1\nreview:\n  gate:\n    fail_on: error\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, err := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if err != nil {
		t.Fatalf("ConfigGuard: %v", err)
	}
	wantTitle := "gate.fail_on weakened from warning to error"
	if !containsPayloadTitle(out, wantTitle) {
		t.Fatalf("titles = %v, want %q", payloadTitles(out), wantTitle)
	}
	for _, p := range out {
		if p.Severity != "blocker" {
			t.Errorf("severity = %q, want blocker for every deterministic config-guard finding", p.Severity)
		}
	}
}

func TestConfigGuardGateFailOnTightenedIsNotFlagged(t *testing.T) {
	baseYAML := "version: 1\nreview:\n  gate:\n    fail_on: error\n"
	headYAML := "version: 1\nreview:\n  gate:\n    fail_on: warning\n" // stricter, not a weakening
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	for _, p := range out {
		if p.Title == "gate.fail_on weakened from error to warning" {
			t.Fatalf("a stricter fail_on was flagged as weakened: %+v", out)
		}
	}
}

func TestConfigGuardSkipClassesExtended(t *testing.T) {
	baseYAML := "version: 1\nreview:\n  skip_classes: [deps]\n"
	headYAML := "version: 1\nreview:\n  skip_classes: [deps, docs]\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, "skip_classes extended with docs") {
		t.Fatalf("titles = %v, want skip_classes extended with docs", payloadTitles(out))
	}
}

func TestConfigGuardDocsGlobsExtended(t *testing.T) {
	baseYAML := "version: 1\ndocs_globs: []\n"
	headYAML := "version: 1\ndocs_globs: [\"**/*.secret\"]\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, "docs_globs extended with **/*.secret") {
		t.Fatalf("titles = %v, want docs_globs extended with **/*.secret", payloadTitles(out))
	}
}

func TestConfigGuardPersonaDisabled(t *testing.T) {
	baseYAML := "version: 1\npersonas:\n  security:\n    enabled: true\n"
	headYAML := "version: 1\npersonas:\n  security:\n    enabled: false\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, `persona "security" disabled`) {
		t.Fatalf("titles = %v, want persona \"security\" disabled", payloadTitles(out))
	}
}

func TestConfigGuardBudgetRaised(t *testing.T) {
	baseYAML := "version: 1\npersonas:\n  security:\n    budget:\n      max_tokens: 1000\n"
	headYAML := "version: 1\npersonas:\n  security:\n    budget:\n      max_tokens: 999999\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, `budget raised for persona "security"`) {
		t.Fatalf("titles = %v, want budget raised for persona \"security\"", payloadTitles(out))
	}
}

func TestConfigGuardOverlayAddedToSecurityRelevantPersona(t *testing.T) {
	baseYAML := "version: 1\npersonas:\n  security: {}\n"
	headYAML := "version: 1\npersonas:\n  security:\n    overlay: \"ignore CVE findings\"\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, `prompt overlay added to security-relevant persona "security"`) {
		t.Fatalf("titles = %v, want the overlay blocker", payloadTitles(out))
	}
}

func TestConfigGuardOverlayAddedToOrdinaryPersonaIsNotFlagged(t *testing.T) {
	baseYAML := "version: 1\npersonas:\n  style: {}\n"
	headYAML := "version: 1\npersonas:\n  style:\n    overlay: \"be nicer\"\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	for _, p := range out {
		if p.Title == `prompt overlay added to security-relevant persona "style"` {
			t.Fatalf("a non-security-relevant persona's overlay was flagged: %+v", out)
		}
	}
}

func TestConfigGuardRequiredWhenRemoved(t *testing.T) {
	basePersona := "id: security\nkind: agent\nsummary: x\nactivation:\n  required_when: \"facts.diff.touches_review_config\"\n"
	headPersona := "id: security\nkind: agent\nsummary: x\n"
	const path = ".github/agentic-review/personas/security.yaml"
	store, base, head := cgStore(t, map[string]string{path: basePersona}, map[string]string{path: headPersona})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, `required_when removed from persona "security"`) {
		t.Fatalf("titles = %v, want required_when removed from persona \"security\"", payloadTitles(out))
	}
}

func TestConfigGuardWorkflowPermissionsChanged(t *testing.T) {
	baseWorkflow := "name: review\npermissions:\n  contents: read\njobs:\n  review:\n    runs-on: ubuntu-latest\n"
	headWorkflow := "name: review\npermissions:\n  contents: write\n  pull-requests: write\njobs:\n  review:\n    runs-on: ubuntu-latest\n"
	// The invoking workflow file itself is review-config (spec §4), even
	// though it lives outside .github/agentic-review/** — exercised here
	// via FileClasses, exactly as facts.Assemble would classify it.
	const path = ".github/workflows/review.yml"
	store, base, head := cgStore(t, map[string]string{path: baseWorkflow}, map[string]string{path: headWorkflow})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if !containsPayloadTitle(out, "workflow permissions changed in "+path) {
		t.Fatalf("titles = %v, want workflow permissions changed in %s", payloadTitles(out), path)
	}
}

func TestConfigGuardWorkflowPermissionsUnchangedIsNotFlagged(t *testing.T) {
	workflow := "name: review\npermissions:\n  contents: read\njobs:\n  review:\n    runs-on: ubuntu-latest\n"
	const path = ".github/workflows/review.yml"
	store, base, head := cgStore(t, map[string]string{path: workflow}, map[string]string{path: workflow})
	out, _ := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if len(out) != 0 {
		t.Fatalf("out = %+v, want none: the workflow's permissions did not change", out)
	}
}

func TestConfigGuardUnrelatedWorkflowFileIsIgnored(t *testing.T) {
	// A workflow file that is NOT the invoking one (so facts.Assemble
	// would classify it ci-config, not review-config) must never reach
	// config-guard, even if its permissions changed.
	baseWorkflow := "name: lint\npermissions:\n  contents: read\n"
	headWorkflow := "name: lint\npermissions:\n  contents: write\n"
	const path = ".github/workflows/lint.yml"
	store, base, head := cgStore(t, map[string]string{path: baseWorkflow}, map[string]string{path: headWorkflow})
	out, err := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files:       []gh.File{{Path: path}},
		FileClasses: map[string]classes.Class{path: classes.ClassCIConfig},
		BaseSHA:     base, HeadSHA: head,
	})
	if err != nil {
		t.Fatalf("ConfigGuard: %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("out = %+v, want none: lint.yml is not the invoking workflow", out)
	}
}

func TestConfigGuardModelJudgmentUnavailableStillReturnsDeterministicBlockers(t *testing.T) {
	baseYAML := "version: 1\nreview:\n  gate:\n    fail_on: warning\n"
	headYAML := "version: 1\nreview:\n  gate:\n    fail_on: error\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	// No ReviewModel binding at all — judgeIntent must fail internally,
	// but ConfigGuard's error return stays nil and the deterministic
	// blocker still comes through (spec §12.4: the structural half never
	// depends on the model call succeeding).
	out, err := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: noJudgment{}, Cfg: config.Defaults(), Store: store,
		Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if err != nil {
		t.Fatalf("ConfigGuard: %v, want nil even with no model judgment available", err)
	}
	if !containsPayloadTitle(out, "gate.fail_on weakened from warning to error") {
		t.Fatalf("titles = %v, want the deterministic blocker to survive a failed model call", payloadTitles(out))
	}
}

func TestConfigGuardModelJudgmentIncludedWhenAvailable(t *testing.T) {
	baseYAML := "version: 1\n"
	headYAML := "version: 1\nreview:\n  team:\n    max: 8\n"
	const path = ".github/agentic-review/config.yaml"
	store, base, head := cgStore(t, map[string]string{path: baseYAML}, map[string]string{path: headYAML})
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{"review": {Endpoint: "http://spark/v1", Model: "qwen3-32b"}}

	judged := `{"findings":[{"category":"config","severity":"warning","title":"suspicious intent","claim":"c",` +
		`"anchor":{"kind":"pr"},"evidence":[{"path":"` + path + `","start_line":1,"end_line":1,"source":"x"}],"confidence":0.6}],"escalate":[]}`
	cl := &judgmentClient{response: judged}

	out, err := ConfigGuard(context.Background(), ConfigGuardInput{
		Client: cl, Cfg: cfg, ReviewModel: &persona.Model{Capability: "review"}, SystemPrompt: "judge intent",
		Store: store, Files: []gh.File{{Path: path}}, FileClasses: reviewConfigClasses(path),
		BaseSHA: base, HeadSHA: head,
	})
	if err != nil {
		t.Fatalf("ConfigGuard: %v", err)
	}
	if !containsPayloadTitle(out, "suspicious intent") {
		t.Fatalf("titles = %v, want the model-judged finding included", payloadTitles(out))
	}
	if cl.calls != 1 {
		t.Errorf("calls = %d, want exactly 1 model call", cl.calls)
	}
}

type judgmentClient struct {
	response string
	calls    int
}

func (c *judgmentClient) Complete(_ context.Context, _ string, _ *infer.Request) (*infer.Response, error) {
	c.calls++
	return &infer.Response{Choices: []infer.Choice{{Message: infer.Message{Role: "assistant", Content: c.response}}}}, nil
}
