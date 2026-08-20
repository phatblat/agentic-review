// Package runner implements tier-1 triage and tier-2 team execution: the
// two stages that actually call models, plus the orchestration that ties
// every other package together into one review run (review.go).
package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// ErrTriageFailed is returned when the triage persona fails schema
// validation after every retry. The caller substitutes the conservative
// fallback roster (logic, security, verifier/groundedness) and proceeds
// with assessment == nil.
var ErrTriageFailed = errors.New("runner: triage failed schema validation after retries")

// maxTriageRetries is spec §16's retry budget on a schema-validation
// failure.
const maxTriageRetries = 2

// TriageInput is the untrusted PR content triage's user message is built
// from; facts (trusted, runtime-assembled) are passed separately.
type TriageInput struct {
	Title   string
	Body    string
	Commits []string
}

// Run runs the exactly-one triage-kind registry persona — persona.Resolve
// rejects zero or more than one at load time — and returns its assessment.
// prompts is the merged builtin+repo-local prompt map (persona.MergePrompts).
func RunTriage(ctx context.Context, cl infer.Client, reg persona.Registry, prompts map[string]string, cfg *config.Config, f *facts.Facts, in TriageInput) (*schema.Assessment, error) {
	rp, err := findTriagePersona(reg)
	if err != nil {
		return nil, err
	}
	if rp.Model == nil {
		return nil, fmt.Errorf("runner: triage persona %q has no model binding", rp.ID)
	}
	binding, ok := cfg.Models[rp.Model.Capability]
	if !ok {
		return nil, fmt.Errorf("runner: triage: capability %q has no models[] binding", rp.Model.Capability)
	}

	factsJSON, err := json.Marshal(f)
	if err != nil {
		return nil, fmt.Errorf("runner: triage: marshal facts: %w", err)
	}

	schemaRaw, err := schema.Raw("triage.v1")
	if err != nil {
		return nil, fmt.Errorf("runner: triage: %w", err)
	}

	messages := []infer.Message{
		{Role: "system", Content: rp.SystemPrompt(prompts)},
		{Role: "user", Content: buildTriageUserContent(factsJSON, in)},
	}

	for attempt := 0; attempt <= maxTriageRetries; attempt++ {
		req := &infer.Request{
			Model:           binding.Model,
			Messages:        messages,
			MaxTokens:       rp.Budget.MaxTokens,
			ReasoningEffort: binding.ReasoningEffort,
			ResponseFormat: &infer.ResponseFormat{
				Type:       "json_schema",
				JSONSchema: infer.JSONSchemaSpec{Name: "triage_v1", Schema: schemaRaw, Strict: true},
			},
		}
		resp, err := cl.Complete(ctx, binding.Endpoint, req)
		if err != nil {
			return nil, fmt.Errorf("runner: triage: %w", err)
		}
		if len(resp.Choices) == 0 {
			return nil, fmt.Errorf("runner: triage: response has no choices")
		}
		content := resp.Choices[0].Message.Content
		truncated := infer.Truncated(resp)

		assessment, verr := schema.DecodeAssessment([]byte(content))
		if verr == nil {
			return assessment, nil
		}

		if attempt == maxTriageRetries {
			if truncated {
				logx.Warn("runner: triage truncated at its %d-token budget on every attempt; raise the triage persona's budget.max_tokens (using fallback roster)", rp.Budget.MaxTokens)
			} else {
				logx.Warn("runner: triage failed after %d retries; using fallback roster", maxTriageRetries)
			}
			return nil, ErrTriageFailed
		}
		if truncated {
			// Restate the request instead of appending the fragment: the
			// ceiling is unchanged, so the only way the next attempt fits
			// is by being shorter, and carrying a truncated answer forward
			// just leaves less room to be shorter in.
			logx.Warn("runner: triage truncated at its %d-token budget; retrying for a terser answer", rp.Budget.MaxTokens)
			messages = append(messages[:2:2], infer.Message{Role: "user", Content: infer.TruncationRetryPrompt})
			continue
		}
		messages = append(messages,
			infer.Message{Role: "assistant", Content: content},
			infer.Message{Role: "user", Content: fmt.Sprintf(
				"Your previous response failed schema validation: %s\n\nRespond again with a single JSON object matching the schema exactly, nothing else.",
				verr.Error())},
		)
	}
	return nil, ErrTriageFailed
}

func findTriagePersona(reg persona.Registry) (*persona.ResolvedPersona, error) {
	var found *persona.ResolvedPersona
	for id, rp := range reg {
		if rp.Kind != persona.KindTriage {
			continue
		}
		if found != nil {
			return nil, fmt.Errorf("runner: registry has more than one triage-kind persona")
		}
		found = reg[id]
	}
	if found == nil {
		return nil, fmt.Errorf("runner: registry has no triage-kind persona")
	}
	return found, nil
}

func buildTriageUserContent(factsJSON []byte, in TriageInput) string {
	var b strings.Builder
	b.WriteString("Facts (runtime-assembled; trusted):\n")
	b.Write(factsJSON)
	b.WriteString("\n\n")
	b.WriteString(infer.WrapUntrusted("pr-title", in.Title))
	b.WriteString("\n\n")
	b.WriteString(infer.WrapUntrusted("pr-body", in.Body))
	if len(in.Commits) > 0 {
		b.WriteString("\n\n")
		b.WriteString(infer.WrapUntrusted("commit-messages", strings.Join(in.Commits, "\n---\n")))
	}
	return b.String()
}
