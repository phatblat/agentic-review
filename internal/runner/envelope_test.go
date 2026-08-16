package runner

import (
	"reflect"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

func TestStampEnvelopeFieldsPopulated(t *testing.T) {
	cfg := config.Defaults()
	cfg.Models = map[string]config.ModelBinding{"review": {Endpoint: "http://spark:8000/v1", Model: "qwen3-32b"}}
	rp := &persona.ResolvedPersona{Definition: persona.Definition{
		ID: "security", Kind: persona.KindAgent, Model: &persona.Model{Capability: "review"},
	}}
	payload := schema.Payload{Category: "security", Severity: "blocker", Title: "t", Claim: "c", Confidence: 0.9}

	f := stampEnvelope(payload, 7, rp, cfg, "abc123")

	if f.Schema != "findings/v1" {
		t.Errorf("Schema = %q, want findings/v1", f.Schema)
	}
	if f.Envelope.ID != "f-0007" {
		t.Errorf("ID = %q, want f-0007 (zero-padded emission index)", f.Envelope.ID)
	}
	if f.Envelope.Persona != "security" {
		t.Errorf("Persona = %q, want security", f.Envelope.Persona)
	}
	if f.Envelope.PersonaKind != "agent" {
		t.Errorf("PersonaKind = %q, want agent", f.Envelope.PersonaKind)
	}
	if f.Envelope.Model != "review/qwen3-32b@spark:8000" {
		t.Errorf("Model = %q, want review/qwen3-32b@spark:8000", f.Envelope.Model)
	}
	if f.Envelope.HeadSHA != "abc123" {
		t.Errorf("HeadSHA = %q, want abc123", f.Envelope.HeadSHA)
	}
	if f.Envelope.Fingerprint == "" {
		t.Errorf("Fingerprint is empty, want a computed fingerprint")
	}
	if f.Envelope.Verification.Disposition != schema.DispositionAccepted {
		t.Errorf("Disposition = %q, want accepted (the default before any lens runs)", f.Envelope.Verification.Disposition)
	}
	if !reflect.DeepEqual(f.Payload, payload) {
		t.Errorf("Payload = %+v, want the original payload untouched", f.Payload)
	}
}

func TestStampEnvelopeDeterministicPersonaHasEmptyModel(t *testing.T) {
	cfg := config.Defaults()
	rp := &persona.ResolvedPersona{Definition: persona.Definition{ID: "dep-risk", Kind: persona.KindDeterministic}}
	payload := schema.Payload{Category: "security", Severity: "blocker", Title: "t", Claim: "c"}

	f := stampEnvelope(payload, 1, rp, cfg, "abc123")

	if f.Envelope.Model != "" {
		t.Errorf("Model = %q, want empty for a model-less deterministic persona", f.Envelope.Model)
	}
}
