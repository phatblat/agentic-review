// Package config holds .github/agentic-review/config.yaml's decoded shape.
// It has no dependency on internal/persona — persona.Resolve depends on
// config, not the other way around, so Budget here is config's own type
// rather than an alias of persona.Budget.
package config

import "github.com/phatblat/agentic-review/internal/classes"

// Config is the strict-decoded shape of config.yaml, loaded from the PR
// head ref (spec §3.1).
type Config struct {
	Version   int                        `yaml:"version"` // must be 1
	Review    Review                     `yaml:"review"`
	DocsGlobs []string                   `yaml:"docs_globs"`
	Models    map[string]ModelBinding    `yaml:"models"`
	Personas  map[string]PersonaOverride `yaml:"personas"`
}

// Review holds every review-shaping setting.
type Review struct {
	Team         Team               `yaml:"team"`
	SkipClasses  []string           `yaml:"skip_classes"`
	SkipWhen     []string           `yaml:"skip_when"` // CEL, facts-only
	Escalation   []Escalation       `yaml:"escalation"`
	Gate         Gate               `yaml:"gate"`
	Caps         map[string]int     `yaml:"caps"` // per-severity comment caps
	Verification ReviewVerification `yaml:"verification"`
}

// Team is the tier-2 team size floor/ceiling.
type Team struct {
	Min int `yaml:"min"`
	Max int `yaml:"max"`
}

// Escalation is one "when a CEL condition holds, add these personas" rule.
type Escalation struct {
	When string   `yaml:"when"`
	Add  []string `yaml:"add"`
}

// Gate holds the exit-code severity threshold.
type Gate struct {
	FailOn string `yaml:"fail_on"`
}

// ReviewVerification holds the materiality-lens floor behavior.
type ReviewVerification struct {
	MaterialityFloor string `yaml:"materiality_floor"` // "downgrade" | "drop"
}

// ModelBinding maps a capability class to an inference endpoint.
type ModelBinding struct {
	Endpoint      string `yaml:"endpoint"`
	Model         string `yaml:"model"`
	ContextWindow int    `yaml:"context_window"` // optional; 0 = unknown
	APIKeyEnv     string `yaml:"api_key_env"`    // default "AGENTIC_REVIEW_API_KEY"
}

// Budget is config's own lower-only override of a persona's token/tool-call
// budget.
type Budget struct {
	MaxTokens    int `yaml:"max_tokens"`
	MaxToolCalls int `yaml:"max_tool_calls"`
}

// PersonaOverride is a repo-local override layered onto a persona
// definition at resolution time (spec §3.1). Pointer fields distinguish
// "unset" from an explicit zero value.
type PersonaOverride struct {
	Enabled     *bool   `yaml:"enabled"`
	Priority    *int    `yaml:"priority"`
	Budget      *Budget `yaml:"budget"`
	MaxFindings *int    `yaml:"max_findings"`
	Overlay     string  `yaml:"overlay"` // append-only prompt overlay
}

// Defaults returns the fields Config uses when config.yaml is absent, or
// leaves a field unset. Every default is applied independently, so a
// config.yaml that sets only Review.Gate still gets every other default.
func Defaults() *Config {
	return &Config{
		Version: 1,
		Review: Review{
			Team:        Team{Min: 1, Max: 5},
			SkipClasses: []string{"deps", "docs"},
			Gate:        Gate{FailOn: "nit"},
			Caps:        map[string]int{"nit": 5, "warning": 10, "error": 20},
			Verification: ReviewVerification{
				MaterialityFloor: "downgrade",
			},
		},
		DocsGlobs: append([]string(nil), classes.DefaultDocsGlobs...),
	}
}
