// Package activation implements spec §5's CEL activation grammar: the
// frozen facts/assessment CEL namespace and function library (env.go), the
// sugar compiler that turns a structured YAML trigger into CEL source
// (sugar.go), the §5.4 context-class lint (lint.go), and rule evaluation
// (eval.go).
//
// Trigger lives here rather than in internal/persona so that persona can
// depend on activation (to compile and lint its rules at load time)
// without activation needing to depend back on persona.
package activation

// Trigger is one activation trigger group; the present keys are AND-ed
// together and compiled to one CEL source string by Compile.
type Trigger struct {
	Paths     []string `yaml:"paths"`
	Languages []string `yaml:"languages"`
	Domains   []string `yaml:"domains"`
	Labels    []string `yaml:"labels"`
	Classes   []string `yaml:"classes"`
	Expr      string   `yaml:"expr"`
}
