package runner

import (
	"fmt"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/verify"
)

// stampEnvelope fills schema.Finding's runtime-owned envelope fields
// immediately after decode (spec §6.2): id (over the run-global emission
// order n), persona, persona_kind, model, head_sha, and fingerprint.
// Nothing model-emitted can forge these — the payload schema is
// additionalProperties: false, so a payload field named envelope, id, or
// fingerprint is a schema violation, never a value that reaches here.
func stampEnvelope(payload schema.Payload, n int, rp *persona.ResolvedPersona, cfg *config.Config, headSHA string) schema.Finding {
	model := ""
	if rp.Model != nil {
		model = roster.ResolvedModel(cfg, rp.Model.Capability)
	}
	return schema.Finding{
		Schema:  "findings/v1",
		Payload: payload,
		Envelope: schema.Envelope{
			ID:          fmt.Sprintf("f-%04d", n),
			Fingerprint: verify.Fingerprint(payload),
			Persona:     rp.ID,
			PersonaKind: string(rp.Kind),
			Model:       model,
			HeadSHA:     headSHA,
			Verification: schema.Verification{
				Disposition: schema.DispositionAccepted,
			},
		},
	}
}
