package schema

import (
	"bytes"
	"embed"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

//go:embed *.schema.json
var schemaFS embed.FS

// names is every embedded schema's $id, doubling as its filename stem.
var names = []string{"triage.v1", "findings.v1", "verdicts.v1"}

// Raw returns the embedded schema document's raw bytes, for
// response_format json_schema and the vLLM guided_json extra.
func Raw(name string) (json.RawMessage, error) {
	data, err := schemaFS.ReadFile(name + ".schema.json")
	if err != nil {
		return nil, fmt.Errorf("schema: read %s: %w", name, err)
	}
	return json.RawMessage(data), nil
}

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

// compileAll compiles every embedded schema once per process; jsonschema/v6
// schemas are safe for concurrent Validate calls once compiled.
func compileAll() (map[string]*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		c := jsonschema.NewCompiler()
		docs := make(map[string]any, len(names))
		for _, name := range names {
			data, err := schemaFS.ReadFile(name + ".schema.json")
			if err != nil {
				compileErr = fmt.Errorf("schema: read %s: %w", name, err)
				return
			}
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compileErr = fmt.Errorf("schema: unmarshal %s: %w", name, err)
				return
			}
			docs[name] = doc
			if err := c.AddResource(name, doc); err != nil {
				compileErr = fmt.Errorf("schema: add resource %s: %w", name, err)
				return
			}
		}
		out := make(map[string]*jsonschema.Schema, len(names))
		for _, name := range names {
			sch, err := c.Compile(name)
			if err != nil {
				compileErr = fmt.Errorf("schema: compile %s: %w", name, err)
				return
			}
			out[name] = sch
		}
		compiled = out
	})
	return compiled, compileErr
}

// Validate validates body (raw JSON) against the named embedded schema.
func Validate(name string, body []byte) error {
	schemas, err := compileAll()
	if err != nil {
		return err
	}
	sch, ok := schemas[name]
	if !ok {
		return fmt.Errorf("schema: unknown schema %q", name)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return fmt.Errorf("schema: decode body for %s: %w", name, err)
	}
	if err := sch.Validate(v); err != nil {
		return fmt.Errorf("schema: %s: %w", name, err)
	}
	return nil
}

// DecodeAssessment validates and decodes a triage/v1 response.
//
// domains and suggested_personas are set-valued, but the schema cannot
// say so: JSON Schema's uniqueItems is rejected outright by the
// grammar-constrained decoding backends this talks to ("Grammar error:
// Unimplemented keys"), and a guided model with room to fill an array
// will happily emit ["ci", "ci", ...] up to maxItems. Deduplication
// therefore lives here, preserving first-occurrence order so the
// artifact reads the way the model ranked it.
func DecodeAssessment(body []byte) (*Assessment, error) {
	if err := Validate("triage.v1", body); err != nil {
		return nil, err
	}
	var out Assessment
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("schema: decode triage.v1: %w", err)
	}
	out.Domains = dedupe(out.Domains)
	out.SuggestedPersonas = dedupe(out.SuggestedPersonas)
	return &out, nil
}

// dedupe returns in's distinct elements in first-occurrence order. A nil
// or already-distinct slice is returned as-is, so the common case
// allocates nothing.
func dedupe(in []string) []string {
	if len(in) < 2 {
		return in
	}
	seen := make(map[string]struct{}, len(in))
	out := in[:0:len(in)]
	for _, s := range in {
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// DecodeFindings validates and decodes a findings/v1 response.
func DecodeFindings(body []byte) (*FindingsResponse, error) {
	if err := Validate("findings.v1", body); err != nil {
		return nil, err
	}
	var out FindingsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("schema: decode findings.v1: %w", err)
	}
	return &out, nil
}

// DecodeVerdicts validates and decodes a verdicts/v1 response.
func DecodeVerdicts(body []byte) (*VerdictsResponse, error) {
	if err := Validate("verdicts.v1", body); err != nil {
		return nil, err
	}
	var out VerdictsResponse
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("schema: decode verdicts.v1: %w", err)
	}
	return &out, nil
}
