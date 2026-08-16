package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/validate"
)

func writeValidateFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

const validateModelsConfig = "version: 1\nmodels:\n" +
	"  triage: {endpoint: \"http://spark/v1\", model: \"m\"}\n" +
	"  review: {endpoint: \"http://spark/v1\", model: \"m\"}\n" +
	"  verify: {endpoint: \"http://spark/v1\", model: \"m\"}\n"

func TestCmdValidateExitsZeroOnCleanBuiltinRoster(t *testing.T) {
	root := t.TempDir()
	writeValidateFile(t, filepath.Join(root, ".github/agentic-review/config.yaml"), validateModelsConfig)

	if exit := cmdValidate(context.Background(), []string{root}); exit != 0 {
		t.Fatalf("cmdValidate = %d, want 0 for the clean builtin roster", exit)
	}
}

func TestCmdValidateExitsOneOnContextClassViolation(t *testing.T) {
	root := t.TempDir()
	writeValidateFile(t, filepath.Join(root, ".github/agentic-review/config.yaml"),
		validateModelsConfig+"review:\n  skip_when: [\"assessment.risk >= RISK_HIGH\"]\n")

	if exit := cmdValidate(context.Background(), []string{root}); exit != 1 {
		t.Fatalf("cmdValidate = %d, want 1 for a skip_when referencing assessment", exit)
	}
}

func TestCmdValidateExitsOneOnUnresolvableConfig(t *testing.T) {
	root := t.TempDir()
	writeValidateFile(t, filepath.Join(root, ".github/agentic-review/config.yaml"), "version: 1\nreview: [not, a, map]\n")

	if exit := cmdValidate(context.Background(), []string{root}); exit != 1 {
		t.Fatalf("cmdValidate = %d, want 1 for malformed config.yaml", exit)
	}
}

func TestCheckLabelFormatsPersonaAndSlot(t *testing.T) {
	got := checkLabel(validate.RuleCheck{PersonaID: "security", Slot: "security.volunteer_on[0]"})
	if got != "security volunteer_on[0]" {
		t.Errorf("checkLabel = %q, want %q", got, "security volunteer_on[0]")
	}
}

func TestCheckLabelHandlesConfigLevelRule(t *testing.T) {
	got := checkLabel(validate.RuleCheck{PersonaID: "", Slot: "skip_when[0]"})
	if got != "skip_when[0]" {
		t.Errorf("checkLabel = %q, want %q", got, "skip_when[0]")
	}
}
