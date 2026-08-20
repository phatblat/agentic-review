package runner

import (
	"context"
	"fmt"
	"sync"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/diffscan"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/handlers"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/osv"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/tools"
)

// TeamDeps bundles every dependency tier-2 team execution needs beyond the
// resolved roster itself.
type TeamDeps struct {
	Client     infer.Client
	Cfg        *config.Config
	Facts      *facts.Facts
	Assessment *schema.Assessment
	Prompts    map[string]string

	Files       []gh.File
	FileClasses map[string]classes.Class // facts.Assemble's second return value
	Store       *gh.ContentStore
	BaseSHA     string
	HeadSHA     string
	PR          PRContext

	OSVClient     *osv.Client
	DepsDevClient *osv.DepsDevClient

	// RecordDir, when non-empty, records every model request/response
	// pair per persona (spec §13.3); "" disables recording.
	RecordDir string
	// ReplayDir, when non-empty, replays recordings from this directory
	// instead of calling Client at all (per-persona, matching Recorder's
	// own file layout) — mutually exclusive with RecordDir; a non-empty
	// ReplayDir always wins.
	ReplayDir string
}

type memberOutcome struct {
	payloads []schema.Payload
	escalate []string
	tokens   int
	err      error
}

// Run executes every roster member concurrently, a semaphore of
// min(len(team), 4); a member failure is captured, logged, and does not
// cancel its peers — this is why errgroup is not used. Deterministic-kind
// members dispatch to internal/handlers instead of the model. A second,
// admission-gated wave runs for any persona id wave 1's agents requested
// via their findings-response "escalate" field (spec §3.4's third
// escalation source): roster.Compute reruns with those ids so the same
// registry/enabled/MaxTeamSize rules and logging that govern config- and
// triage-driven escalation govern this one too. Results are collected
// into a slice ordered by (persona-id, emission index) — the final
// roster's Members list is ID-sorted by roster.Compute — so artifacts
// are byte-stable across runs.
func RunTeam(ctx context.Context, deps TeamDeps, reg persona.Registry, rst *roster.Roster) ([]schema.Finding, *roster.Budget, error) {
	coverage := diffscan.ScanFiles(deps.Files)
	diffPaths := make(map[string]bool, len(deps.Files))
	for _, f := range deps.Files {
		diffPaths[f.Path] = true
	}

	outcomes := runWave(ctx, deps, reg, rst.Members)

	finalRoster := rst
	var escalateIDs []string
	for _, m := range rst.Members {
		escalateIDs = append(escalateIDs, outcomes[m.ID].escalate...)
	}
	if len(escalateIDs) > 0 {
		rst2, err := roster.Compute(reg, deps.Facts, deps.Assessment, deps.Cfg, escalateIDs...)
		if err != nil {
			logx.Warn("runner: team: wave 2 roster recompute failed: %v", err)
		} else {
			finalRoster = rst2
			var newMembers []roster.Member
			for _, m := range rst2.Members {
				if _, already := outcomes[m.ID]; !already {
					newMembers = append(newMembers, m)
				}
			}
			for id, oc := range runWave(ctx, deps, reg, newMembers) {
				outcomes[id] = oc
			}
		}
	}

	budget := &roster.Budget{Allocated: map[string]int{}, Consumed: map[string]int{}}
	for _, m := range finalRoster.Members {
		budget.Allocated[m.ID] = m.MaxTokens
	}

	var all []schema.Finding
	n := 0
	for _, m := range finalRoster.Members {
		oc, ran := outcomes[m.ID]
		if !ran {
			continue // wave 2 recompute failed; m never got dispatched
		}
		budget.Consumed[m.ID] = oc.tokens
		if oc.err != nil {
			logx.Warn("runner: team: %s: %v", m.ID, oc.err)
			continue
		}
		rp := reg[m.ID]

		stamped := make([]schema.Finding, 0, len(oc.payloads))
		for _, p := range oc.payloads {
			n++
			stamped = append(stamped, stampEnvelope(p, n, rp, deps.Cfg, deps.HeadSHA))
		}

		maxFindings := rp.Output.MaxFindings
		if maxFindings <= 0 || maxFindings > persona.MaxFindingsPerPersona {
			maxFindings = persona.MaxFindingsPerPersona
		}
		stamped = mechanicalValidate(ctx, stamped, deps.Store, coverage, diffPaths, deps.HeadSHA, deps.BaseSHA, maxFindings)

		all = append(all, stamped...)
	}

	return all, budget, nil
}

// runWave runs every given member concurrently, a semaphore of
// min(len(members), 4); a member failure is captured and does not
// cancel its peers.
func runWave(ctx context.Context, deps TeamDeps, reg persona.Registry, members []roster.Member) map[string]memberOutcome {
	outcomes := make(map[string]memberOutcome, len(members))
	if len(members) == 0 {
		return outcomes
	}
	var mu sync.Mutex
	sem := make(chan struct{}, min(len(members), 4))
	var wg sync.WaitGroup
	for _, m := range members {
		rp, ok := reg[m.ID]
		if !ok {
			outcomes[m.ID] = memberOutcome{err: fmt.Errorf("%s is not in the registry", m.ID)}
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(id string, rp *persona.ResolvedPersona) {
			defer wg.Done()
			defer func() { <-sem }()
			payloads, escalate, tokens, err := runMember(ctx, deps, rp)
			mu.Lock()
			outcomes[id] = memberOutcome{payloads: payloads, escalate: escalate, tokens: tokens, err: err}
			mu.Unlock()
		}(m.ID, rp)
	}
	wg.Wait()
	return outcomes
}

// runMember dispatches one roster member's turn: a deterministic-kind
// persona to internal/handlers, an agent-kind persona to the model.
func runMember(ctx context.Context, deps TeamDeps, rp *persona.ResolvedPersona) ([]schema.Payload, []string, int, error) {
	if rp.Kind == persona.KindDeterministic {
		payloads, err := runDeterministic(ctx, deps, rp)
		return payloads, nil, 0, err
	}
	return runAgent(ctx, deps, rp)
}

func runDeterministic(ctx context.Context, deps TeamDeps, rp *persona.ResolvedPersona) ([]schema.Payload, error) {
	if rp.Runtime == nil {
		return nil, fmt.Errorf("deterministic persona %q has no runtime.handler", rp.ID)
	}
	switch rp.Runtime.Handler {
	case "builtin/dep-risk":
		return handlers.DepRisk(ctx, deps.OSVClient, deps.DepsDevClient, deps.Facts.Deps.Changed), nil
	case "builtin/config-guard":
		return handlers.ConfigGuard(ctx, handlers.ConfigGuardInput{
			Client:       infer.Select(deps.Client, deps.ReplayDir, deps.RecordDir, rp.ID),
			Cfg:          deps.Cfg,
			ReviewModel:  rp.Model,
			SystemPrompt: rp.SystemPrompt(deps.Prompts),
			Store:        deps.Store,
			Files:        deps.Files,
			FileClasses:  deps.FileClasses,
			BaseSHA:      deps.BaseSHA,
			HeadSHA:      deps.HeadSHA,
			PRTitle:      deps.PR.Title,
			PRBody:       deps.PR.Body,
		})
	default:
		return nil, fmt.Errorf("unknown deterministic handler %q", rp.Runtime.Handler)
	}
}

const (
	maxAgentTurns   = 30 // safety valve against a runaway tool-calling loop
	maxAgentRetries = 2  // spec §16's retry budget on a schema-validation failure
)

func runAgent(ctx context.Context, deps TeamDeps, rp *persona.ResolvedPersona) ([]schema.Payload, []string, int, error) {
	if rp.Model == nil {
		return nil, nil, 0, fmt.Errorf("agent persona %q has no model binding", rp.ID)
	}
	binding, ok := deps.Cfg.Models[rp.Model.Capability]
	if !ok {
		return nil, nil, 0, fmt.Errorf("capability %q has no models[] binding", rp.Model.Capability)
	}

	schemaRaw, err := schema.Raw("findings.v1")
	if err != nil {
		return nil, nil, 0, err
	}

	userContent, err := buildScopedInput(ctx, rp, deps.Facts, deps.Files, deps.PR, deps.Store, deps.BaseSHA, deps.HeadSHA)
	if err != nil {
		return nil, nil, 0, err
	}

	messages := []infer.Message{
		{Role: "system", Content: rp.SystemPrompt(deps.Prompts)},
		{Role: "user", Content: userContent},
	}

	toolDefs := tools.Definitions(rp.Inputs.Tools)
	var toolRegistry *tools.Registry
	if len(rp.Inputs.Tools) > 0 {
		toolRegistry = tools.NewRegistry(deps.Store, deps.HeadSHA, deps.OSVClient, rp.Budget.MaxToolCalls)
	}

	cl := infer.Select(deps.Client, deps.ReplayDir, deps.RecordDir, rp.ID)

	totalTokens := 0
	retries := 0

	for turn := 0; turn < maxAgentTurns; turn++ {
		req := &infer.Request{
			Model:     binding.Model,
			Messages:  messages,
			MaxTokens: rp.Budget.MaxTokens,
			ResponseFormat: &infer.ResponseFormat{
				Type:       "json_schema",
				JSONSchema: infer.JSONSchemaSpec{Name: "findings_v1", Schema: schemaRaw, Strict: true},
			},
		}
		if len(toolDefs) > 0 {
			req.Tools = toolDefs
			req.ToolChoice = "auto"
		}

		resp, err := cl.Complete(ctx, binding.Endpoint, req)
		if err != nil {
			return nil, nil, totalTokens, err
		}
		totalTokens += resp.Usage.TotalTokens
		if len(resp.Choices) == 0 {
			return nil, nil, totalTokens, fmt.Errorf("response has no choices")
		}
		msg := resp.Choices[0].Message

		if len(msg.ToolCalls) > 0 {
			messages = append(messages, msg)
			for _, tc := range msg.ToolCalls {
				result := `{"error":"tool calling is not configured for this persona"}`
				if toolRegistry != nil {
					result = toolRegistry.Call(ctx, tc.Function.Name, tc.Function.Arguments)
				}
				messages = append(messages, infer.Message{Role: "tool", ToolCallID: tc.ID, Content: result})
			}
			continue
		}

		findingsResp, verr := schema.DecodeFindings([]byte(msg.Content))
		if verr == nil {
			return findingsResp.Findings, findingsResp.Escalate, totalTokens, nil
		}

		if retries >= maxAgentRetries {
			return nil, nil, totalTokens, fmt.Errorf("schema validation failed after %d retries: %w", maxAgentRetries, verr)
		}
		retries++
		messages = append(messages,
			infer.Message{Role: "assistant", Content: msg.Content},
			infer.Message{Role: "user", Content: fmt.Sprintf(
				"Your previous response failed schema validation: %s\n\nRespond again with a single JSON object matching the schema exactly, nothing else.",
				verr.Error())},
		)
	}
	return nil, nil, totalTokens, fmt.Errorf("exceeded %d turns without a final structured response", maxAgentTurns)
}
