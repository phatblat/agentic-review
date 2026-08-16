package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
)

// keyedClient dispatches a scripted findings.v1 response per model name, so
// a multi-persona test can give each persona a distinct response without
// them racing over a shared call counter.
type keyedClient struct {
	responses map[string]string // model -> findings.v1 JSON
	errors    map[string]error
}

func (k *keyedClient) Complete(_ context.Context, _ string, req *infer.Request) (*infer.Response, error) {
	if err, ok := k.errors[req.Model]; ok {
		return nil, err
	}
	content, ok := k.responses[req.Model]
	if !ok {
		return nil, fmt.Errorf("keyedClient: no scripted response for model %q", req.Model)
	}
	return &infer.Response{
		Choices: []infer.Choice{{Message: infer.Message{Role: "assistant", Content: content}}},
		Usage:   infer.Usage{TotalTokens: 42},
	}, nil
}

func teamTestCfg(models map[string]string) *config.Config {
	cfg := config.Defaults()
	cfg.Review.Team.Max = persona.MaxTeamSize
	cfg.Models = map[string]config.ModelBinding{}
	for capability, model := range models {
		cfg.Models[capability] = config.ModelBinding{Endpoint: "http://spark/v1", Model: model}
	}
	return cfg
}

// teamTestStore builds a ContentStore backed by an on-disk fixture whose
// head/f.go content byte-matches every findingsJSON evidence entry, so
// agent findings survive mechanicalValidate's evidence check unmolested.
func teamTestStore(t *testing.T) (*gh.ContentStore, string) {
	t.Helper()
	dir := t.TempDir()
	writeTeamFile(t, filepath.Join(dir, "pr.json"), `{"number":1,"head_sha":"headsha"}`)
	writeTeamFile(t, filepath.Join(dir, "head", "f.go"), "x\n")
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	return gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"}), "headsha"
}

func writeTeamFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func syntheticTriageDef() persona.Definition {
	return persona.Definition{ID: "triage", Kind: persona.KindTriage, Summary: "synthetic triage for team tests"}
}

func resolveTeam(t *testing.T, defs []persona.Definition, cfg *config.Config) persona.Registry {
	t.Helper()
	reg, err := persona.Resolve(append([]persona.Definition{syntheticTriageDef()}, defs...), nil, cfg)
	if err != nil {
		t.Fatalf("persona.Resolve: %v", err)
	}
	return reg
}

func agentDef(id, capability string, maxTokens int) persona.Definition {
	return persona.Definition{
		ID:         id,
		Kind:       persona.KindAgent,
		Summary:    "x",
		Activation: persona.Activation{Always: true},
		Model:      &persona.Model{Capability: capability},
		Budget:     persona.Budget{MaxTokens: maxTokens},
	}
}

func findingsJSON(title string, escalate ...string) string {
	esc := "[]"
	if len(escalate) > 0 {
		esc = `["` + strings.Join(escalate, `","`) + `"]`
	}
	return fmt.Sprintf(`{"findings":[{"category":"correctness","severity":"warning","title":%q,"claim":"c",`+
		`"anchor":{"kind":"pr"},"evidence":[{"path":"f.go","start_line":1,"end_line":1,"source":"x"}],"confidence":0.5}],"escalate":%s}`, title, esc)
}

func TestRunTeamConcurrentAgentsSucceed(t *testing.T) {
	defs := []persona.Definition{
		agentDef("alpha", "cap-a", 1000),
		agentDef("beta", "cap-b", 1000),
	}
	cfg := teamTestCfg(map[string]string{"cap-a": "model-a", "cap-b": "model-b"})
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}
	if len(rst.Members) != 2 {
		t.Fatalf("roster has %d members, want 2", len(rst.Members))
	}

	cl := &keyedClient{responses: map[string]string{
		"model-a": findingsJSON("alpha finding"),
		"model-b": findingsJSON("beta finding"),
	}}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}

	findings, budget, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v", err)
	}
	if len(findings) != 2 {
		t.Fatalf("got %d findings, want 2: %+v", len(findings), findings)
	}
	gotTitles := map[string]bool{}
	for _, f := range findings {
		gotTitles[f.Payload.Title] = true
		if f.Envelope.ID == "" || f.Envelope.Fingerprint == "" {
			t.Errorf("finding %+v missing envelope stamp", f)
		}
	}
	if !gotTitles["alpha finding"] || !gotTitles["beta finding"] {
		t.Errorf("titles = %v, want both alpha and beta findings", gotTitles)
	}
	if budget.Consumed["alpha"] != 42 || budget.Consumed["beta"] != 42 {
		t.Errorf("budget.Consumed = %+v, want 42 tokens for each", budget.Consumed)
	}
	if budget.Allocated["alpha"] != 1000 || budget.Allocated["beta"] != 1000 {
		t.Errorf("budget.Allocated = %+v, want 1000 for each", budget.Allocated)
	}
}

func TestRunTeamMemberFailureDoesNotCancelPeers(t *testing.T) {
	defs := []persona.Definition{
		agentDef("broken", "cap-broken", 1000),
		agentDef("healthy", "cap-healthy", 1000),
	}
	cfg := teamTestCfg(map[string]string{"cap-broken": "model-broken", "cap-healthy": "model-healthy"})
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	cl := &keyedClient{
		responses: map[string]string{"model-healthy": findingsJSON("healthy finding")},
		errors:    map[string]error{"model-broken": errors.New("endpoint unreachable")},
	}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}

	findings, _, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v, want a nil top-level error even though one member failed", err)
	}
	if len(findings) != 1 || findings[0].Payload.Title != "healthy finding" {
		t.Fatalf("findings = %+v, want exactly the healthy persona's finding", findings)
	}
}

func TestRunTeamDeterministicDispatch(t *testing.T) {
	defs := []persona.Definition{{
		ID:         "dep-risk",
		Kind:       persona.KindDeterministic,
		Summary:    "x",
		Activation: persona.Activation{Always: true},
		Runtime:    &persona.Runtime{Handler: "builtin/dep-risk"},
		Budget:     persona.Budget{MaxTokens: 0},
	}}
	cfg := teamTestCfg(nil)
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	deps := TeamDeps{Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}}
	findings, _, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v", err)
	}
	if len(findings) != 0 {
		t.Fatalf("findings = %+v, want none: facts.deps.changed is empty so dep-risk emits nothing", findings)
	}
}

func TestRunTeamWave2EscalationAdmitted(t *testing.T) {
	defs := []persona.Definition{
		agentDef("scout", "cap-scout", 1000),
		agentDef("specialist", "cap-specialist", 1000), // volunteers on nothing; only reachable via escalation
	}
	defs[1].Activation = persona.Activation{} // not Always: only escalation can add it
	cfg := teamTestCfg(map[string]string{"cap-scout": "model-scout", "cap-specialist": "model-specialist"})
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}
	if len(rst.Members) != 1 {
		t.Fatalf("initial roster = %+v, want only scout (specialist has no activation rule)", rst.Members)
	}

	cl := &keyedClient{responses: map[string]string{
		"model-scout":      findingsJSON("scout finding", "specialist"),
		"model-specialist": findingsJSON("specialist finding"),
	}}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}

	findings, budget, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v", err)
	}
	gotTitles := map[string]bool{}
	for _, f := range findings {
		gotTitles[f.Payload.Title] = true
	}
	if !gotTitles["scout finding"] || !gotTitles["specialist finding"] {
		t.Fatalf("titles = %v, want scout's escalate to admit and run specialist", gotTitles)
	}
	if _, ok := budget.Allocated["specialist"]; !ok {
		t.Errorf("budget.Allocated = %+v, want an entry for the escalated specialist", budget.Allocated)
	}
}

func TestRunTeamWave2EscalationDeniedAtMaxTeamSize(t *testing.T) {
	var defs []persona.Definition
	models := map[string]string{}
	for i := 0; i < persona.MaxTeamSize; i++ {
		id := fmt.Sprintf("member-%d", i)
		defs = append(defs, agentDef(id, id, 1000))
		models[id] = id
	}
	extra := agentDef("extra", "extra", 1000)
	extra.Activation = persona.Activation{} // unreachable except via escalation
	defs = append(defs, extra)
	models["extra"] = "extra"

	cfg := teamTestCfg(models)
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}
	if len(rst.Members) != persona.MaxTeamSize {
		t.Fatalf("initial roster has %d members, want the full %d-member ceiling", len(rst.Members), persona.MaxTeamSize)
	}

	responses := map[string]string{"extra": findingsJSON("extra finding")}
	for i := 0; i < persona.MaxTeamSize; i++ {
		id := fmt.Sprintf("member-%d", i)
		esc := []string{}
		if i == 0 {
			esc = []string{"extra"} // only one member requests the escalation
		}
		responses[id] = findingsJSON(id+" finding", esc...)
	}
	cl := &keyedClient{responses: responses}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}

	findings, _, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v", err)
	}
	for _, f := range findings {
		if f.Payload.Title == "extra finding" {
			t.Fatalf("findings include the escalated persona despite MaxTeamSize already being reached: %+v", findings)
		}
	}
	if len(findings) != persona.MaxTeamSize {
		t.Fatalf("got %d findings, want exactly %d (one per original member, escalation denied)", len(findings), persona.MaxTeamSize)
	}
}
