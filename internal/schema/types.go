// Package schema is the single source of truth for every model-facing JSON
// contract: an embedded JSON Schema file, used both for guided decoding
// (Raw) and for validating the response (Validate), plus the Go type each
// contract decodes into. Field tags mirror spec §6.1–§6.3 exactly, with
// `cel:` tags on Assessment so internal/activation can register it in the
// CEL environment.
package schema

// Risk is the triage assessment's ordered risk enum, low..critical.
type Risk int

const (
	RiskLow Risk = iota
	RiskModerate
	RiskHigh
	RiskCritical
)

var riskStrings = [...]string{RiskLow: "low", RiskModerate: "moderate", RiskHigh: "high", RiskCritical: "critical"}

func (r Risk) String() string {
	if r >= 0 && int(r) < len(riskStrings) {
		return riskStrings[r]
	}
	return "low"
}

func (r Risk) MarshalJSON() ([]byte, error) { return marshalEnumString(r.String()) }

func (r *Risk) UnmarshalJSON(data []byte) error {
	s, err := unmarshalEnumString(data)
	if err != nil {
		return err
	}
	for i, v := range riskStrings {
		if v == s {
			*r = Risk(i)
			return nil
		}
	}
	return enumError("risk", s)
}

// Cmplx is the triage assessment's ordered complexity enum,
// trivial..complex. Named Cmplx, not Complexity, to keep it visually
// distinct from the Complexity field that holds it.
type Cmplx int

const (
	ComplexityTrivial Cmplx = iota
	ComplexitySimple
	ComplexityModerate
	ComplexityComplex
)

var complexityStrings = [...]string{
	ComplexityTrivial: "trivial", ComplexitySimple: "simple",
	ComplexityModerate: "moderate", ComplexityComplex: "complex",
}

func (c Cmplx) String() string {
	if c >= 0 && int(c) < len(complexityStrings) {
		return complexityStrings[c]
	}
	return "trivial"
}

func (c Cmplx) MarshalJSON() ([]byte, error) { return marshalEnumString(c.String()) }

func (c *Cmplx) UnmarshalJSON(data []byte) error {
	s, err := unmarshalEnumString(data)
	if err != nil {
		return err
	}
	for i, v := range complexityStrings {
		if v == s {
			*c = Cmplx(i)
			return nil
		}
	}
	return enumError("complexity", s)
}

// Domains is the closed spec §6.3 domain enum ("where"), shared by the
// triage assessment and optional finding payloads.
var Domains = []string{
	"auth", "secrets", "network", "storage", "concurrency",
	"api-surface", "ui", "build", "ci", "dependencies", "data-handling",
}

// Categories is the closed spec §6.3 finding category enum ("what's
// wrong").
var Categories = []string{
	"correctness", "security", "performance", "testing", "docs", "style", "config", "o11y", "i18n",
}

// Severities is the closed severity enum, in ascending order.
var Severities = []string{"nit", "warning", "error", "blocker"}

// Assessment is the triage/v1 model output. Facts are runtime-assembled and
// never model-emitted, so only the assessment itself round-trips through
// guided decoding.
type Assessment struct {
	Risk              Risk     `cel:"risk"               json:"risk"`
	Complexity        Cmplx    `cel:"complexity"         json:"complexity"`
	Domains           []string `cel:"domains"            json:"domains"`
	Summary           string   `cel:"summary"            json:"summary"`
	Rationale         string   `cel:"rationale"          json:"rationale"`
	SuggestedPersonas []string `cel:"suggested_personas" json:"suggested_personas"`
	Confidence        float64  `cel:"confidence"         json:"confidence"`
}

// AnchorKind is where a finding is anchored.
type AnchorKind string

const (
	AnchorLine AnchorKind = "line"
	AnchorFile AnchorKind = "file"
	AnchorPR   AnchorKind = "pr"
)

// AnchorRef selects which side of the diff an anchor's lines are on.
type AnchorRef string

const (
	RefHead AnchorRef = "head"
	RefBase AnchorRef = "base" // deleted code
)

// Anchor locates a finding in the diff.
type Anchor struct {
	Kind      AnchorKind `json:"kind"`
	Path      string     `json:"path,omitempty"`
	StartLine int        `json:"start_line,omitempty"`
	EndLine   int        `json:"end_line,omitempty"`
	Ref       AnchorRef  `json:"ref,omitempty"`
}

// Evidence is one byte-matchable excerpt supporting a finding's claim.
type Evidence struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	Source    string `json:"source"`
}

// SuggestedFix is a candidate replacement for the anchored lines.
type SuggestedFix struct {
	StartLine   int    `json:"start_line"`
	EndLine     int    `json:"end_line"`
	Replacement string `json:"replacement"`
}

// Payload is a finding's model-emitted content — everything the findings.v1
// schema validates. It never carries provenance or verdict fields; those
// live in Envelope, which the runtime stamps after decode.
type Payload struct {
	Category     string        `json:"category"`
	Severity     string        `json:"severity"`
	Title        string        `json:"title"`
	Claim        string        `json:"claim"`
	Domains      []string      `json:"domains,omitempty"`
	Anchor       Anchor        `json:"anchor"`
	Evidence     []Evidence    `json:"evidence,omitempty"`
	SuggestedFix *SuggestedFix `json:"suggested_fix,omitempty"`
	Confidence   float64       `json:"confidence"`
}

// Disposition is a finding's final fate after verification.
type Disposition string

const (
	DispositionAccepted   Disposition = "accepted"
	DispositionDropped    Disposition = "dropped"
	DispositionDowngraded Disposition = "downgraded"
	DispositionMerged     Disposition = "merged"
)

// EnvelopeVerdict is one lens's recorded verdict on a finding.
type EnvelopeVerdict struct {
	Lens    string `json:"lens"`
	Result  string `json:"result"`  // "pass" | "fail"
	Checked string `json:"checked"` // "mechanical" | "model" | "mechanical+model"
	Reason  string `json:"reason,omitempty"`
}

// Verification is a finding's accumulated verification trace.
type Verification struct {
	Verdicts    []EnvelopeVerdict `json:"verdicts,omitempty"`
	Disposition Disposition       `json:"disposition"`
}

// Posted records where a finding landed once posted to GitHub.
type Posted struct {
	CommentID int64 `json:"comment_id,omitempty"`
}

// Envelope is everything the runtime stamps onto a Payload after decode.
// Nothing model-emitted can forge these fields.
type Envelope struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
	Persona     string `json:"persona"`
	PersonaKind string `json:"persona_kind"`
	Model       string `json:"model"`
	HeadSHA     string `json:"head_sha"`
	// MergedPersonas lists every contributing persona id when the
	// duplication lens merged two or more findings into this one
	// (spec item 35); empty when this finding was never merged.
	MergedPersonas []string     `json:"merged_personas,omitempty"`
	Verification   Verification `json:"verification"`
	Posted         *Posted      `json:"posted,omitempty"`
}

// Finding is a model-emitted Payload inside a runtime-stamped Envelope.
type Finding struct {
	Schema   string   `json:"schema"` // "findings/v1"
	Payload  Payload  `json:"payload"`
	Envelope Envelope `json:"envelope"`
}

// FindingsResponse is a findings/v1 guided-decoding call's direct response:
// Payload-shaped findings only (no envelope) plus optional escalation
// requests.
type FindingsResponse struct {
	Findings []Payload `json:"findings"`
	Escalate []string  `json:"escalate"`
}

// ModelVerdict is one model-emitted verdict from a verdicts/v1 call.
type ModelVerdict struct {
	FindingID string `json:"finding_id"`
	Result    string `json:"result"` // "pass" | "fail"
	Reason    string `json:"reason"`
}

// VerdictsResponse is a verdicts/v1 guided-decoding call's direct response.
type VerdictsResponse struct {
	Verdicts []ModelVerdict `json:"verdicts"`
}
