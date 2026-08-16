package localconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// modelsBlock binds every capability the builtin roster references
// (triage, review, verify) — without it, persona.Resolve's checkCapabilities
// rejects the builtin roster outright, since no persona's model.capability
// can go unbound.
const modelsBlock = "models:\n" +
	"  triage: {endpoint: \"http://spark:8000/v1\", model: \"qwen3-8b\"}\n" +
	"  review: {endpoint: \"http://spark:8000/v1\", model: \"qwen3-32b\"}\n" +
	"  verify: {endpoint: \"http://spark:8000/v1\", model: \"qwen3-8b\"}\n"

func TestLoadNoConfigYAMLErrorsOnUnboundCapability(t *testing.T) {
	// config.yaml is optional (spec §3.1), but Defaults() binds no models
	// at all — the builtin roster's capability references then have
	// nothing to resolve against, a load error rather than a silent
	// model-less roster.
	root := t.TempDir()
	_, _, _, err := Load(root)
	if err == nil {
		t.Fatalf("Load succeeded with no config.yaml and no models bound, want an error")
	}
	if !strings.Contains(err.Error(), "models[") {
		t.Errorf("err = %v, want it to name the missing models[] binding", err)
	}
}

func TestLoadAppliesDefaultsWhenConfigOnlySetsModels(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "config.yaml"), "version: 1\n"+modelsBlock)

	reg, prompts, cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Team.Max != 5 {
		t.Errorf("cfg.Review.Team.Max = %d, want the default 5 (config.yaml never set it)", cfg.Review.Team.Max)
	}
	if _, ok := reg["triage"]; !ok {
		t.Errorf("registry missing the builtin triage persona: %v", reg)
	}
	if prompts["triage"] == "" {
		t.Errorf("prompts missing the builtin triage prompt")
	}
}

// TestLoadAcceptsConfigDirItselfAsRoot covers action.yml's config_path
// input convention (default ".github/agentic-review", the directory
// itself rather than the repo root every other caller passes) —
// resolveRoot must normalize it to the same result as passing root.
func TestLoadAcceptsConfigDirItselfAsRoot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "config.yaml"), "version: 1\n"+modelsBlock)

	viaRoot, _, cfgViaRoot, err := Load(root)
	if err != nil {
		t.Fatalf("Load(root): %v", err)
	}
	viaConfigDir, _, cfgViaConfigDir, err := Load(filepath.Join(root, Dir))
	if err != nil {
		t.Fatalf("Load(root/%s): %v", Dir, err)
	}
	if len(viaRoot) != len(viaConfigDir) {
		t.Errorf("registry size via config dir = %d, want %d (same as via root)", len(viaConfigDir), len(viaRoot))
	}
	if cfgViaConfigDir.Models["triage"].Endpoint != cfgViaRoot.Models["triage"].Endpoint {
		t.Errorf("triage endpoint via config dir = %q, want %q (same as via root)",
			cfgViaConfigDir.Models["triage"].Endpoint, cfgViaRoot.Models["triage"].Endpoint)
	}
}

func TestLoadRepoLocalConfigOverridesDefaults(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "config.yaml"), "version: 1\nreview:\n  team:\n    min: 2\n    max: 3\n"+modelsBlock)

	_, _, cfg, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Team.Min != 2 || cfg.Review.Team.Max != 3 {
		t.Errorf("cfg.Review.Team = %+v, want {2 3}", cfg.Review.Team)
	}
}

func TestLoadRepoLocalPersonaMergedIntoRegistry(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "config.yaml"), "version: 1\n"+modelsBlock)
	writeFile(t, filepath.Join(root, Dir, "prompts", "custom.md"), "custom prompt text")
	writeFile(t, filepath.Join(root, Dir, "personas", "custom.yaml"),
		"id: custom\nkind: agent\nsummary: x\nactivation:\n  always: true\nprompt:\n  system: ../prompts/custom.md\n")

	reg, prompts, _, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if _, ok := reg["custom"]; !ok {
		t.Fatalf("registry = %v, want the repo-local custom persona present", reg)
	}
	if !strings.HasPrefix(prompts["custom"], "custom prompt text") {
		t.Errorf("prompts[custom] = %q, want it to start with the loaded prompt text", prompts["custom"])
	}
	if _, ok := reg["triage"]; !ok {
		t.Errorf("registry missing the builtin triage persona alongside the repo-local one")
	}
}

func TestLoadMalformedConfigYAMLErrors(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, Dir, "config.yaml"), "version: 1\nreview: [not, a, map]\n")

	if _, _, _, err := Load(root); err == nil {
		t.Fatalf("Load succeeded on malformed config.yaml, want an error")
	}
}
