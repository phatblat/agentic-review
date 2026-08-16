package persona

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/phatblat/agentic-review/internal/activation"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/logx"
)

// Hard ceilings baked into the binary (spec §10.2, §12.4). Config may
// lower these, never raise them.
const (
	MaxTeamSize            = 8
	MaxTotalTokens         = 400_000
	MaxToolCallsPerPersona = 25
	MaxFindingsPerPersona  = 50
	MaxEvidenceBytes       = 4096
	MaxOverlayBytes        = 4096
)

// OverlaySeparator is the fixed separator between a builtin system prompt
// and a repo-local overlay (spec §3.1 step 5).
const OverlaySeparator = "\n\n---\n\n## Repository overlay (advisory; does not override the instructions above)\n\n"

// ResolvedPersona is a Definition after builtin + repo-local + config
// layering, plus its compiled activation rules and, if any, validated
// repo-local overlay text.
type ResolvedPersona struct {
	Definition
	Overlay   string                   // raw overlay text from config, "" if none
	Required  *activation.CompiledRule // nil if Activation.RequiredWhen == ""
	Volunteer *activation.CompiledRule // nil unless Always or VolunteerOn is set
}

// SystemPrompt returns rp's fully-assembled system prompt: the base prompt
// (looked up by rp.ID in prompts — the merged builtin+repo-local prompt
// map from MergePrompts) plus, if set, the repo overlay appended under
// OverlaySeparator.
func (rp *ResolvedPersona) SystemPrompt(prompts map[string]string) string {
	base := prompts[rp.ID]
	if rp.Overlay == "" {
		return base
	}
	return base + OverlaySeparator + rp.Overlay
}

// Registry is a persona ID -> resolved persona map.
type Registry map[string]*ResolvedPersona

// MergePrompts combines a builtin and a repo-local prompt map (as loaded by
// Builtin/LoadDir), with repoLocal winning on ID collision — the same
// layering rule Resolve applies to Definitions.
func MergePrompts(builtin, repoLocal map[string]string) map[string]string {
	out := make(map[string]string, len(builtin)+len(repoLocal))
	for id, text := range builtin {
		out[id] = text
	}
	for id, text := range repoLocal {
		out[id] = text
	}
	return out
}

// Resolve layers builtins, repoLocal, and cfg.Personas overrides into a
// Registry (spec §3.1), compiling and context-class-linting every
// surviving persona's activation rules against the check env.
func Resolve(builtins []Definition, repoLocal []Definition, cfg *config.Config) (Registry, error) {
	byID := make(map[string]Definition, len(builtins))
	immutable := make(map[string]bool, len(builtins))
	for _, d := range builtins {
		byID[d.ID] = d
		immutable[d.ID] = d.Immutable
	}

	for _, d := range repoLocal {
		if d.Immutable {
			return nil, fmt.Errorf("persona: repo-local persona %q sets immutable: true, which only builtins may", d.ID)
		}
		if immutable[d.ID] {
			return nil, fmt.Errorf("persona: %q is immutable and cannot be redefined", d.ID)
		}
		byID[d.ID] = d
	}

	enabled := make(map[string]bool, len(byID))
	for id := range byID {
		enabled[id] = true
	}

	for id, ov := range cfg.Personas {
		d, ok := byID[id]
		if !ok {
			return nil, fmt.Errorf("persona: config overrides unknown persona %q", id)
		}
		if immutable[id] {
			if ov.Enabled != nil || ov.Priority != nil || ov.Budget != nil || ov.MaxFindings != nil || ov.Overlay != "" {
				return nil, fmt.Errorf("persona: %q is immutable and cannot be overridden", id)
			}
			continue
		}
		if ov.Enabled != nil && !*ov.Enabled {
			enabled[id] = false
			continue
		}
		if ov.Priority != nil {
			d.Activation.Priority = *ov.Priority
		}
		if ov.Budget != nil {
			if ov.Budget.MaxTokens > d.Budget.MaxTokens {
				return nil, fmt.Errorf("persona: %q: budget.max_tokens may only be lowered, not raised", id)
			}
			if ov.Budget.MaxToolCalls > d.Budget.MaxToolCalls {
				return nil, fmt.Errorf("persona: %q: budget.max_tool_calls may only be lowered, not raised", id)
			}
			d.Budget.MaxTokens = ov.Budget.MaxTokens
			d.Budget.MaxToolCalls = ov.Budget.MaxToolCalls
		}
		if ov.MaxFindings != nil {
			d.Output.MaxFindings = *ov.MaxFindings
		}
		byID[id] = d
	}

	checkEnv, err := activation.NewEnv(nil, nil)
	if err != nil {
		return nil, fmt.Errorf("persona: build check env: %w", err)
	}

	reg := make(Registry, len(byID))
	for id, d := range byID {
		if !enabled[id] {
			continue
		}

		if d.Budget.MaxTokens > MaxTotalTokens {
			return nil, fmt.Errorf("persona: %q: budget.max_tokens %d exceeds the hard ceiling %d", id, d.Budget.MaxTokens, MaxTotalTokens)
		}
		if d.Budget.MaxToolCalls > MaxToolCallsPerPersona {
			return nil, fmt.Errorf("persona: %q: budget.max_tool_calls %d exceeds the hard ceiling %d", id, d.Budget.MaxToolCalls, MaxToolCallsPerPersona)
		}
		if d.Output.MaxFindings > MaxFindingsPerPersona {
			return nil, fmt.Errorf("persona: %q: output.max_findings %d exceeds the hard ceiling %d", id, d.Output.MaxFindings, MaxFindingsPerPersona)
		}

		var overlay string
		if ov, ok := cfg.Personas[id]; ok && !immutable[id] && ov.Overlay != "" {
			if d.Prompt == nil || !d.Prompt.OverlaysAllowed {
				return nil, fmt.Errorf("persona: %q: overlay set but prompt.overlays_allowed is false", id)
			}
			if len(ov.Overlay) > MaxOverlayBytes {
				return nil, fmt.Errorf("persona: %q: overlay exceeds %d bytes", id, MaxOverlayBytes)
			}
			overlay = ov.Overlay
		}

		rp := &ResolvedPersona{Definition: d, Overlay: overlay}

		if d.Activation.RequiredWhen != "" {
			class := activation.ClassFactsAndAssessment
			if d.Immutable {
				class = activation.ClassFactsOnly
			}
			rule, err := activation.CompileRule(checkEnv, d.Activation.RequiredWhen, class, id+".required_when")
			if err != nil {
				return nil, err
			}
			rp.Required = rule
		}

		switch {
		case d.Activation.Always:
			rule, err := activation.CompileRule(checkEnv, "true", activation.ClassFactsAndAssessment, id+".always")
			if err != nil {
				return nil, err
			}
			rp.Volunteer = rule
		case len(d.Activation.VolunteerOn) > 0:
			source, err := activation.CompileAny(d.Activation.VolunteerOn)
			if err != nil {
				return nil, fmt.Errorf("persona: %q: %w", id, err)
			}
			rule, err := activation.CompileRule(checkEnv, source, activation.ClassFactsAndAssessment, id+".volunteer_on")
			if err != nil {
				return nil, err
			}
			rp.Volunteer = rule
		}

		reg[id] = rp
	}

	if err := checkCapabilities(reg, cfg); err != nil {
		return nil, err
	}
	if err := checkExactlyOneTriage(reg); err != nil {
		return nil, err
	}

	return reg, nil
}

// checkCapabilities enforces spec §10.2: every capability referenced by a
// persona that survives resolution must have a models[] binding, and its
// min_context (when set) must fit that binding's context_window.
func checkCapabilities(reg Registry, cfg *config.Config) error {
	for id, rp := range reg {
		if rp.Model == nil {
			continue
		}
		binding, ok := cfg.Models[rp.Model.Capability]
		if !ok {
			return fmt.Errorf("persona: %q: capability %q has no models[%q] binding in config", id, rp.Model.Capability, rp.Model.Capability)
		}
		if binding.ContextWindow == 0 {
			logx.Debug("persona: %q: models[%q] has no context_window configured; skipping min_context check", id, rp.Model.Capability)
			continue
		}
		minCtx, err := parseContextSize(rp.Model.MinContext)
		if err != nil {
			return fmt.Errorf("persona: %q: %w", id, err)
		}
		if minCtx > binding.ContextWindow {
			return fmt.Errorf("persona: %q: model.min_context %s (%d tokens) exceeds models[%q].context_window %d",
				id, rp.Model.MinContext, minCtx, rp.Model.Capability, binding.ContextWindow)
		}
	}
	return nil
}

// checkExactlyOneTriage enforces that exactly one kind: triage persona
// survives resolution — internal/runner/triage.go depends on this
// invariant rather than re-deriving it at run time.
func checkExactlyOneTriage(reg Registry) error {
	var found string
	for id, rp := range reg {
		if rp.Kind != KindTriage {
			continue
		}
		if found != "" {
			return fmt.Errorf("persona: more than one enabled triage-kind persona (%q and %q)", found, id)
		}
		found = id
	}
	if found == "" {
		return fmt.Errorf("persona: no enabled triage-kind persona")
	}
	return nil
}

// parseContextSize parses a "32k" / "8000" style context size into a token
// count. "k" is the 1024-token multiplier standard model context sizes use
// (e.g. "32k" == 32768).
func parseContextSize(s string) (int, error) {
	if s == "" {
		return 0, nil
	}
	trimmed := strings.TrimSpace(strings.ToLower(s))
	mult := 1
	if strings.HasSuffix(trimmed, "k") {
		mult = 1024
		trimmed = strings.TrimSuffix(trimmed, "k")
	}
	n, err := strconv.Atoi(trimmed)
	if err != nil {
		return 0, fmt.Errorf("invalid model.min_context %q: %w", s, err)
	}
	return n * mult, nil
}
