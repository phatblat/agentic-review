package persona

import "testing"

const validYAML = `
id: security
kind: agent
summary: reviews security-relevant changes
activation:
  volunteer_on:
    - paths: ["**/auth/**"]
model:
  capability: review
inputs:
  scope: matched-files
  context: [pr-metadata]
  tools: [read_file]
output:
  schema: findings/v1
budget:
  max_tokens: 8000
verification:
  required: true
  lenses: [groundedness]
`

func TestParseDefinitionValid(t *testing.T) {
	d, err := ParseDefinition("security.yaml", []byte(validYAML))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if d.ID != "security" || d.Kind != KindAgent {
		t.Errorf("d = %+v", d)
	}
	if len(d.Activation.VolunteerOn) != 1 || d.Activation.VolunteerOn[0].Paths[0] != "**/auth/**" {
		t.Errorf("VolunteerOn = %+v", d.Activation.VolunteerOn)
	}
}

func TestParseDefinitionRejectsUnknownKey(t *testing.T) {
	yaml := validYAML + "unknown_field: true\n"
	if _, err := ParseDefinition("security.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an unknown top-level key")
	}
}

func TestParseDefinitionRejectsBadID(t *testing.T) {
	yaml := `
id: Security_Bad
kind: agent
summary: x
`
	if _, err := ParseDefinition("bad.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an id outside the charset")
	}
}

func TestParseDefinitionRejectsBadKind(t *testing.T) {
	yaml := `
id: foo
kind: wizard
summary: x
`
	if _, err := ParseDefinition("bad.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an invalid kind")
	}
}

func TestParseDefinitionRejectsBadScope(t *testing.T) {
	yaml := `
id: foo
kind: agent
summary: x
inputs:
  scope: everything
`
	if _, err := ParseDefinition("bad.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an invalid inputs.scope")
	}
}

func TestParseDefinitionDeterministicRequiresKnownHandler(t *testing.T) {
	yaml := `
id: foo
kind: deterministic
summary: x
runtime:
  handler: builtin/does-not-exist
`
	if _, err := ParseDefinition("bad.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an unknown runtime.handler")
	}
}

func TestParseDefinitionVerifierRequiresValidLens(t *testing.T) {
	yaml := `
id: verifier/bogus
kind: verifier
summary: x
lens: nonsense
`
	if _, err := ParseDefinition("bad.yaml", []byte(yaml)); err == nil {
		t.Fatalf("ParseDefinition accepted an invalid verifier lens")
	}
}

func TestParseDefinitionNamespacedID(t *testing.T) {
	yaml := `
id: verifier/groundedness
kind: verifier
summary: x
lens: groundedness
`
	d, err := ParseDefinition("ok.yaml", []byte(yaml))
	if err != nil {
		t.Fatalf("ParseDefinition: %v", err)
	}
	if d.ID != "verifier/groundedness" {
		t.Errorf("ID = %q", d.ID)
	}
}
