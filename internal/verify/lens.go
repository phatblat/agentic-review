// Package verify implements spec §7's verification lenses (groundedness,
// injection, duplication, materiality) and §6.2's fingerprint. Lenses run
// in a fixed order after mechanical validation (internal/runner's M7
// stage): drop unfounded and hostile findings before spending tokens on
// merge and floor judgment.
package verify

import (
	"context"
	"fmt"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/tools"
)

// Verdict is one lens's recorded judgment, appended to a finding's
// envelope.verification.verdicts and to verdicts.json.
type Verdict struct {
	Lens    string `json:"lens"`
	Result  string `json:"result"`  // "pass" | "fail"
	Checked string `json:"checked"` // "mechanical" | "model" | "mechanical+model"
	Reason  string `json:"reason"`
}

// Lens is one verification stage. Apply receives every finding still
// under consideration — including ones already dropped or merged by an
// earlier lens, which Apply must pass through unchanged rather than
// re-judge — and returns the same-length, same-order slice with any
// findings it touched updated in place.
type Lens interface {
	Name() string
	Apply(ctx context.Context, in []schema.Finding, env Env) ([]schema.Finding, []Verdict, error)
}

// Env bundles what every lens needs: the model client and config every
// verify-capability call uses, the persona registry (verifier personas
// carry their own system prompt and model binding, looked up by a fixed
// "verifier/<lens>" id), and the content store for groundedness's
// read_file tool.
type Env struct {
	Client  infer.Client
	Cfg     *config.Config
	Reg     persona.Registry
	Prompts map[string]string
	Store   *gh.ContentStore
	HeadSHA string
	IsFork  bool
	// Facts is the tier-0 fact model — materiality's user content
	// includes PR size/risk/complexity so the model can judge
	// proportionality.
	Facts *facts.Facts

	// RecordDir, when non-empty, records every model request/response
	// pair per verifier persona (spec §13.3); "" disables recording.
	RecordDir string
	// ReplayDir, when non-empty, replays recordings from this directory
	// instead of calling Client at all — mutually exclusive with
	// RecordDir; a non-empty ReplayDir always wins.
	ReplayDir string
}

// Order is spec §7's fixed, non-configurable lens sequence.
func Order() []Lens {
	return []Lens{Groundedness{}, Injection{}, Duplication{}, Materiality{}}
}

// Run applies every lens in Order over findings, feeding each lens's
// output into the next. A lens returning an error fails the whole verify
// stage closed — verification that cannot complete must never be treated
// as having passed, especially for the fork-mandatory injection lens.
func Run(ctx context.Context, findings []schema.Finding, env Env) ([]schema.Finding, []Verdict, error) {
	current := findings
	var all []Verdict
	for _, lens := range Order() {
		out, verdicts, err := lens.Apply(ctx, current, env)
		if err != nil {
			return nil, nil, fmt.Errorf("verify: %s: %w", lens.Name(), err)
		}
		current = out
		all = append(all, verdicts...)
	}
	return current, all, nil
}

// acceptedOnly returns the indices of in whose disposition is still
// accepted — the only findings worth spending a lens's tokens on.
func acceptedOnly(in []schema.Finding) []int {
	var idx []int
	for i, f := range in {
		if f.Envelope.Verification.Disposition == schema.DispositionAccepted {
			idx = append(idx, i)
		}
	}
	return idx
}

const (
	maxVerifyTurns   = 10 // groundedness's read_file loop; injection/materiality never call a tool
	maxVerifyRetries = 2  // spec §16's retry budget on a schema-validation failure
)

// callVerifyBatch runs personaID's verify-capability turn over subjects,
// rendered by render, and returns each finding's verdict keyed by
// envelope.id. personaID's inputs.tools (only verifier/groundedness
// declares any) drives whether the tool-calling loop is active.
func callVerifyBatch(ctx context.Context, env Env, personaID string, subjects []schema.Finding, userContent string) (map[string]schema.ModelVerdict, error) {
	rp, ok := env.Reg[personaID]
	if !ok {
		return nil, fmt.Errorf("%s not in registry", personaID)
	}
	if rp.Model == nil {
		return nil, fmt.Errorf("%s has no model binding", personaID)
	}
	binding, ok := env.Cfg.Models[rp.Model.Capability]
	if !ok {
		return nil, fmt.Errorf("capability %q has no models[] binding", rp.Model.Capability)
	}

	schemaRaw, err := schema.Raw("verdicts.v1")
	if err != nil {
		return nil, err
	}

	toolDefs := tools.Definitions(rp.Inputs.Tools)
	var toolRegistry *tools.Registry
	if len(rp.Inputs.Tools) > 0 {
		toolRegistry = tools.NewRegistry(env.Store, env.HeadSHA, nil, rp.Budget.MaxToolCalls)
	}

	cl := infer.Select(env.Client, env.ReplayDir, env.RecordDir, personaID)

	messages := []infer.Message{
		{Role: "system", Content: rp.SystemPrompt(env.Prompts)},
		{Role: "user", Content: userContent},
	}

	retries := 0
	for turn := 0; turn < maxVerifyTurns; turn++ {
		req := &infer.Request{
			Model:     binding.Model,
			Messages:  messages,
			MaxTokens: rp.Budget.MaxTokens,
			ResponseFormat: &infer.ResponseFormat{
				Type:       "json_schema",
				JSONSchema: infer.JSONSchemaSpec{Name: "verdicts_v1", Schema: schemaRaw, Strict: true},
			},
		}
		if len(toolDefs) > 0 {
			req.Tools = toolDefs
			req.ToolChoice = "auto"
		}

		resp, err := cl.Complete(ctx, binding.Endpoint, req)
		if err != nil {
			return nil, err
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("%s: response has no choices", personaID)
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

		verdictsResp, verr := schema.DecodeVerdicts([]byte(msg.Content))
		if verr == nil {
			out := make(map[string]schema.ModelVerdict, len(verdictsResp.Verdicts))
			for _, v := range verdictsResp.Verdicts {
				out[v.FindingID] = v
			}
			return out, nil
		}

		if retries >= maxVerifyRetries {
			return nil, fmt.Errorf("%s: schema validation failed after %d retries: %w", personaID, maxVerifyRetries, verr)
		}
		retries++
		messages = append(messages,
			infer.Message{Role: "assistant", Content: msg.Content},
			infer.Message{Role: "user", Content: fmt.Sprintf(
				"Your previous response failed schema validation: %s\n\nRespond again with a single JSON object matching the schema exactly, nothing else.",
				verr.Error())},
		)
	}
	return nil, fmt.Errorf("%s: exceeded %d turns without a final structured response", personaID, maxVerifyTurns)
}

// logMissingVerdict warns when the model's batch response omits a finding
// the caller sent it — the finding is left untouched by the caller rather
// than treated as a pass or a fail.
func logMissingVerdict(lens, findingID string) {
	logx.Warn("verify: %s: batch response omitted finding %s; leaving it unjudged by this lens", lens, findingID)
}
