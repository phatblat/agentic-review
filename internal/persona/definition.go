// Package persona holds the YAML shape of a persona definition (spec §3.3),
// the builtin roster embedded in the binary, repo-local loading, and
// resolution (builtin + repo-local + config layering, spec §3.1).
package persona

import (
	"fmt"
	"regexp"

	"github.com/goccy/go-yaml"

	"github.com/phatblat/agentic-review/internal/activation"
)

// Kind is a persona's execution kind.
type Kind string

const (
	KindAgent         Kind = "agent"
	KindDeterministic Kind = "deterministic"
	KindVerifier      Kind = "verifier"
	KindTriage        Kind = "triage"
)

// Lens is a verifier persona's fixed lens (kind == verifier only).
type Lens string

const (
	LensGroundedness Lens = "groundedness"
	LensMateriality  Lens = "materiality"
	LensDuplication  Lens = "duplication"
	LensInjection    Lens = "injection"
)

// Definition is the strict-decoded YAML shape of one persona.
type Definition struct {
	ID           string       `yaml:"id"`
	Kind         Kind         `yaml:"kind"`
	Summary      string       `yaml:"summary"`
	Immutable    bool         `yaml:"immutable"` // rejected outside builtins
	Lens         Lens         `yaml:"lens"`      // kind==verifier only
	Activation   Activation   `yaml:"activation"`
	Model        *Model       `yaml:"model"`
	Runtime      *Runtime     `yaml:"runtime"`
	Inputs       Inputs       `yaml:"inputs"`
	Prompt       *Prompt      `yaml:"prompt"`
	Output       Output       `yaml:"output"`
	Budget       Budget       `yaml:"budget"`
	Verification Verification `yaml:"verification"`
}

// Activation is a persona's activation rules. VolunteerOn's element type
// lives in internal/activation, not here, so both packages can depend on
// it without persona and activation importing each other.
type Activation struct {
	Always       bool                 `yaml:"always"`
	VolunteerOn  []activation.Trigger `yaml:"volunteer_on"`
	RequiredWhen string               `yaml:"required_when"`
	Priority     int                  `yaml:"priority"`
	Excludes     []string             `yaml:"excludes"`
}

// Model names the capability class this persona binds to.
type Model struct {
	Capability       string `yaml:"capability"`
	MinContext       string `yaml:"min_context"`       // "32k"; parsed to tokens
	StructuredOutput string `yaml:"structured_output"` // "findings/v1"
}

// Runtime names a deterministic persona's handler. v1 only supports
// "builtin/<name>" handlers.
type Runtime struct {
	Handler string `yaml:"handler"`
}

// Inputs controls what content and tools a persona's turn is built from.
type Inputs struct {
	Scope   string   `yaml:"scope"` // matched-files|full-diff|full-files|metadata-only
	Context []string `yaml:"context"`
	Tools   []string `yaml:"tools"`
}

// Prompt names the persona's system prompt file.
type Prompt struct {
	System          string `yaml:"system"` // path relative to the persona file
	OverlaysAllowed bool   `yaml:"overlays_allowed"`
}

// Output controls a persona's findings output shape.
type Output struct {
	Schema      string   `yaml:"schema"`
	Severities  []string `yaml:"severities"`
	MaxFindings int      `yaml:"max_findings"`
}

// Budget caps a persona's per-run resource consumption.
type Budget struct {
	MaxTokens    int `yaml:"max_tokens"`
	MaxToolCalls int `yaml:"max_tool_calls"`
}

// Verification names which lenses a persona's findings must pass.
type Verification struct {
	Required bool   `yaml:"required"`
	Lenses   []Lens `yaml:"lenses"`
}

// idPattern is spec §3.2's persona ID charset.
var idPattern = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*(/[a-z0-9]+(-[a-z0-9]+)*)*$`)

// validKinds, validLenses, validScopes, validContexts, validTools, and
// validHandlers are the closed value sets enforced at load time.
var (
	validKinds = map[Kind]bool{
		KindAgent: true, KindDeterministic: true, KindVerifier: true, KindTriage: true,
	}
	validLenses = map[Lens]bool{
		LensGroundedness: true, LensMateriality: true, LensDuplication: true, LensInjection: true,
	}
	validScopes = map[string]bool{
		"matched-files": true, "full-diff": true, "full-files": true, "metadata-only": true,
	}
	validContexts = map[string]bool{
		"pr-metadata": true, "pr-body": true, "commit-messages": true,
		"file-contents-head": true, "file-contents-base": true,
	}
	validTools = map[string]bool{
		"read_file": true, "osv_lookup": true,
	}
	validHandlers = map[string]bool{
		"builtin/dep-risk": true, "builtin/config-guard": true,
	}
)

// ParseDefinition strict-decodes one persona YAML document (unknown keys are
// load errors) and validates every closed value set. filename is used only
// for error messages.
func ParseDefinition(filename string, data []byte) (*Definition, error) {
	var d Definition
	if err := yaml.UnmarshalWithOptions(data, &d, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("persona: %s: %w", filename, err)
	}
	if err := d.validate(); err != nil {
		return nil, fmt.Errorf("persona: %s: %w", filename, err)
	}
	return &d, nil
}

func (d *Definition) validate() error {
	if !idPattern.MatchString(d.ID) {
		return fmt.Errorf("id %q does not match %s", d.ID, idPattern.String())
	}
	if !validKinds[d.Kind] {
		return fmt.Errorf("persona %q: invalid kind %q", d.ID, d.Kind)
	}
	if d.Kind == KindVerifier && !validLenses[d.Lens] {
		return fmt.Errorf("persona %q: invalid lens %q", d.ID, d.Lens)
	}
	if d.Inputs.Scope != "" && !validScopes[d.Inputs.Scope] {
		return fmt.Errorf("persona %q: invalid inputs.scope %q", d.ID, d.Inputs.Scope)
	}
	for _, c := range d.Inputs.Context {
		if !validContexts[c] {
			return fmt.Errorf("persona %q: invalid inputs.context %q", d.ID, c)
		}
	}
	for _, t := range d.Inputs.Tools {
		if !validTools[t] {
			return fmt.Errorf("persona %q: invalid inputs.tools %q", d.ID, t)
		}
	}
	if d.Kind == KindDeterministic {
		if d.Runtime == nil || !validHandlers[d.Runtime.Handler] {
			handler := ""
			if d.Runtime != nil {
				handler = d.Runtime.Handler
			}
			return fmt.Errorf("persona %q: invalid runtime.handler %q", d.ID, handler)
		}
	}
	for _, l := range d.Verification.Lenses {
		if !validLenses[l] {
			return fmt.Errorf("persona %q: invalid verification.lenses entry %q", d.ID, l)
		}
	}
	return nil
}
