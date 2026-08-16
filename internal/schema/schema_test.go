package schema

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestValidateAssessmentValid(t *testing.T) {
	body := []byte(`{
		"risk": "high", "complexity": "moderate", "domains": ["auth"],
		"summary": "s", "rationale": "r", "suggested_personas": ["security"],
		"confidence": 0.82
	}`)
	if err := Validate("triage.v1", body); err != nil {
		t.Fatalf("Validate valid assessment: %v", err)
	}
	a, err := DecodeAssessment(body)
	if err != nil {
		t.Fatalf("DecodeAssessment: %v", err)
	}
	if a.Risk != RiskHigh || a.Complexity != ComplexityModerate {
		t.Errorf("decoded = %+v, want risk=high complexity=moderate", a)
	}
}

func TestValidateAssessmentRejectsUnknownEnum(t *testing.T) {
	body := []byte(`{
		"risk": "extreme", "complexity": "moderate", "domains": [],
		"summary": "s", "rationale": "r", "suggested_personas": [],
		"confidence": 0.5
	}`)
	if err := Validate("triage.v1", body); err == nil {
		t.Fatalf("Validate accepted an unknown risk enum value")
	}
}

func TestValidateAssessmentRejectsExtraField(t *testing.T) {
	body := []byte(`{
		"risk": "low", "complexity": "trivial", "domains": [],
		"summary": "s", "rationale": "r", "suggested_personas": [],
		"confidence": 0.1, "extra": "nope"
	}`)
	if err := Validate("triage.v1", body); err == nil {
		t.Fatalf("Validate accepted an additional property")
	}
}

func TestValidateFindingsRequiresEvidence(t *testing.T) {
	body := []byte(`{
		"findings": [{
			"category": "security", "severity": "error", "title": "t", "claim": "c",
			"anchor": {"kind": "line", "path": "a.go", "start_line": 1, "end_line": 1, "ref": "head"},
			"evidence": [],
			"confidence": 0.9
		}],
		"escalate": []
	}`)
	if err := Validate("findings.v1", body); err == nil {
		t.Fatalf("Validate accepted an agent finding with no evidence")
	}
}

func TestValidateFindingsValid(t *testing.T) {
	body := []byte(`{
		"findings": [{
			"category": "security", "severity": "error", "title": "t", "claim": "c",
			"anchor": {"kind": "line", "path": "a.go", "start_line": 1, "end_line": 1, "ref": "head"},
			"evidence": [{"path": "a.go", "start_line": 1, "end_line": 1, "source": "x"}],
			"confidence": 0.9
		}],
		"escalate": ["security"]
	}`)
	resp, err := DecodeFindings(body)
	if err != nil {
		t.Fatalf("DecodeFindings: %v", err)
	}
	if len(resp.Findings) != 1 || resp.Findings[0].Category != "security" {
		t.Errorf("resp = %+v, want one security finding", resp)
	}
}

func TestValidateVerdicts(t *testing.T) {
	body := []byte(`{"verdicts": [{"finding_id": "f-0001", "result": "pass", "reason": "ok"}]}`)
	resp, err := DecodeVerdicts(body)
	if err != nil {
		t.Fatalf("DecodeVerdicts: %v", err)
	}
	if len(resp.Verdicts) != 1 || resp.Verdicts[0].Result != "pass" {
		t.Errorf("resp = %+v", resp)
	}
}

// TestEnumsMatchSchema asserts every closed enum in the embedded schemas
// equals the corresponding Go constant slice, so the two can never drift.
func TestEnumsMatchSchema(t *testing.T) {
	raw, err := Raw("triage.v1")
	if err != nil {
		t.Fatalf("Raw(triage.v1): %v", err)
	}
	var doc struct {
		Properties struct {
			Risk struct {
				Enum []string `json:"enum"`
			} `json:"risk"`
			Complexity struct {
				Enum []string `json:"enum"`
			} `json:"complexity"`
			Domains struct {
				Items struct {
					Enum []string `json:"enum"`
				} `json:"items"`
			} `json:"domains"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("unmarshal triage.v1: %v", err)
	}

	if !reflect.DeepEqual(doc.Properties.Risk.Enum, riskStrings[:]) {
		t.Errorf("schema risk enum = %v, want %v", doc.Properties.Risk.Enum, riskStrings)
	}
	if !reflect.DeepEqual(doc.Properties.Complexity.Enum, complexityStrings[:]) {
		t.Errorf("schema complexity enum = %v, want %v", doc.Properties.Complexity.Enum, complexityStrings)
	}
	if !reflect.DeepEqual(doc.Properties.Domains.Items.Enum, Domains) {
		t.Errorf("schema domains enum = %v, want %v", doc.Properties.Domains.Items.Enum, Domains)
	}

	findingsRaw, err := Raw("findings.v1")
	if err != nil {
		t.Fatalf("Raw(findings.v1): %v", err)
	}
	var findingsDoc struct {
		Properties struct {
			Findings struct {
				Items struct {
					Properties struct {
						Category struct {
							Enum []string `json:"enum"`
						} `json:"category"`
						Severity struct {
							Enum []string `json:"enum"`
						} `json:"severity"`
					} `json:"properties"`
				} `json:"items"`
			} `json:"findings"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(findingsRaw, &findingsDoc); err != nil {
		t.Fatalf("unmarshal findings.v1: %v", err)
	}
	if !reflect.DeepEqual(findingsDoc.Properties.Findings.Items.Properties.Category.Enum, Categories) {
		t.Errorf("schema category enum = %v, want %v", findingsDoc.Properties.Findings.Items.Properties.Category.Enum, Categories)
	}
	gotSeverities := findingsDoc.Properties.Findings.Items.Properties.Severity.Enum
	wantSeverities := []string{"blocker", "error", "warning", "nit"}
	if !reflect.DeepEqual(gotSeverities, wantSeverities) {
		t.Errorf("schema severity enum = %v, want %v", gotSeverities, wantSeverities)
	}
}
