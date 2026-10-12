package runner

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
)

// userCapture wraps keyedClient and remembers the last user message each
// model was sent, so a test can assert what a persona was actually told.
type userCapture struct {
	keyedClient
	mu    sync.Mutex
	users map[string]string // model -> user message
}

func (u *userCapture) Complete(ctx context.Context, endpoint string, req *infer.Request) (*infer.Response, error) {
	u.mu.Lock()
	if u.users == nil {
		u.users = map[string]string{}
	}
	for _, m := range req.Messages {
		if m.Role == "user" {
			u.users[req.Model] = m.Content
		}
	}
	u.mu.Unlock()
	return u.keyedClient.Complete(ctx, endpoint, req)
}

func TestPersonaIDsBlockNamesFieldAndIDs(t *testing.T) {
	got := personaIDsBlock("escalate", []string{"logic", "security"})
	for _, want := range []string{"`escalate`", "trusted): logic, security.", "leave `escalate` empty"} {
		if !strings.Contains(got, want) {
			t.Errorf("block = %q, want it to contain %q", got, want)
		}
	}
}

// With no other persona to name, the block must say so instead of
// rendering an empty list the model could read as "anything goes".
func TestPersonaIDsBlockEmptyListSaysNone(t *testing.T) {
	got := personaIDsBlock("escalate", nil)
	want := "Valid persona ids for `escalate` (runtime-assembled; trusted): none. Leave `escalate` empty."
	if got != want {
		t.Errorf("block = %q, want %q", got, want)
	}
}

// idsLine returns the single "Valid persona ids for `field`" line of a
// user message, so assertions are scoped to it rather than to the facts
// and diff text around it.
func idsLine(t *testing.T, user, field string) string {
	t.Helper()
	prefix := "Valid persona ids for `" + field + "`"
	for _, line := range strings.Split(user, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("user message has no %q line: %q", prefix, user)
	return ""
}

func TestTriageUserMessageListsTeamPersonasOnly(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{
		"triage": {Endpoint: "http://spark/v1", Model: "m-triage"},
		"review": {Endpoint: "http://spark/v1", Model: "m-review"},
	}
	defs := []persona.Definition{
		{ID: "triage", Kind: persona.KindTriage, Summary: "x", Model: &persona.Model{Capability: "triage"}, Budget: persona.Budget{MaxTokens: 2000}},
		agentDef("security", "review", 1000),
		agentDef("logic", "review", 1000),
	}
	reg, err := persona.Resolve(defs, nil, cfg)
	if err != nil {
		t.Fatalf("persona.Resolve: %v", err)
	}

	cl := &scriptedClient{responses: []string{validTriageJSON}}
	if _, err := RunTriage(context.Background(), cl, reg, map[string]string{"triage": "sys"}, cfg, &facts.Facts{}, TriageInput{Title: "t"}); err != nil {
		t.Fatalf("RunTriage: %v", err)
	}

	user := cl.requests[0][1].Content
	if want := "`suggested_personas` (runtime-assembled; trusted): logic, security."; !strings.Contains(user, want) {
		t.Errorf("triage user message = %q, want it to contain %q", user, want)
	}
}

// Each agent is shown the other team personas, never itself and never the
// triage persona. This is what stops models inventing ids like
// "security-reviewer" that roster then denies as not in registry.
func TestAgentUserMessageListsOtherTeamPersonas(t *testing.T) {
	defs := []persona.Definition{
		agentDef("alpha", "cap-a", 1000),
		agentDef("beta", "cap-b", 1000),
		agentDef("gamma", "cap-g", 1000),
	}
	cfg := teamTestCfg(map[string]string{"cap-a": "model-a", "cap-b": "model-b", "cap-g": "model-g"})
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	cl := &userCapture{keyedClient: keyedClient{responses: map[string]string{
		"model-a": findingsJSON("a"),
		"model-b": findingsJSON("b"),
		"model-g": findingsJSON("g"),
	}}}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}
	if _, _, err := RunTeam(context.Background(), deps, reg, rst); err != nil {
		t.Fatalf("RunTeam: %v", err)
	}

	for model, want := range map[string]string{
		"model-a": "trusted): beta, gamma.",
		"model-b": "trusted): alpha, gamma.",
		"model-g": "trusted): alpha, beta.",
	} {
		line := idsLine(t, cl.users[model], "escalate")
		if !strings.Contains(line, want) {
			t.Errorf("%s ids line = %q, want it to contain %q", model, line, want)
		}
		if strings.Contains(line, "triage") {
			t.Errorf("%s ids line offers the triage persona, which escalation can never add: %q", model, line)
		}
	}
}

// An escalated (wave 2) agent is shown the list too. Each agent is offered
// the other, never itself; scout stays offered to specialist because
// tryEscalate ignores ids already on the team.
func TestWave2AgentUserMessageListsOtherTeamPersonas(t *testing.T) {
	defs := []persona.Definition{
		agentDef("scout", "cap-scout", 1000),
		agentDef("specialist", "cap-specialist", 1000),
	}
	defs[1].Activation = persona.Activation{} // only reachable via escalation
	cfg := teamTestCfg(map[string]string{"cap-scout": "model-scout", "cap-specialist": "model-specialist"})
	reg := resolveTeam(t, defs, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	cl := &userCapture{keyedClient: keyedClient{responses: map[string]string{
		"model-scout":      findingsJSON("scout finding", "specialist"),
		"model-specialist": findingsJSON("specialist finding"),
	}}}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}
	if _, _, err := RunTeam(context.Background(), deps, reg, rst); err != nil {
		t.Fatalf("RunTeam: %v", err)
	}

	if _, ok := cl.users["model-specialist"]; !ok {
		t.Fatal("specialist never ran; wave 2 was not exercised")
	}
	for model, want := range map[string]string{
		"model-scout":      "trusted): specialist.",
		"model-specialist": "trusted): scout.",
	} {
		if line := idsLine(t, cl.users[model], "escalate"); !strings.Contains(line, want) {
			t.Errorf("%s ids line = %q, want it to contain %q", model, line, want)
		}
	}
}

// A lone agent has no other persona to name; runAgent must render the
// explicit "none" line, not an empty list.
func TestLoneAgentUserMessageSaysNone(t *testing.T) {
	cfg := teamTestCfg(map[string]string{"cap-solo": "model-solo"})
	reg := resolveTeam(t, []persona.Definition{agentDef("solo", "cap-solo", 1000)}, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	cl := &userCapture{keyedClient: keyedClient{responses: map[string]string{"model-solo": findingsJSON("s")}}}
	store, headSHA := teamTestStore(t)
	deps := TeamDeps{Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{}, Store: store, HeadSHA: headSHA}
	if _, _, err := RunTeam(context.Background(), deps, reg, rst); err != nil {
		t.Fatalf("RunTeam: %v", err)
	}

	want := "Valid persona ids for `escalate` (runtime-assembled; trusted): none. Leave `escalate` empty."
	if got := idsLine(t, cl.users["model-solo"], "escalate"); got != want {
		t.Errorf("ids line = %q, want %q", got, want)
	}
}
