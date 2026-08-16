package config

import "testing"

func TestLoadEmptyReturnsDefaults(t *testing.T) {
	cfg, err := Load(nil)
	if err != nil {
		t.Fatalf("Load(nil): %v", err)
	}
	if cfg.Review.Team.Max != 5 || cfg.Review.Gate.FailOn != "nit" {
		t.Errorf("cfg = %+v, want Defaults()", cfg)
	}
}

func TestLoadPartialMergesDefaults(t *testing.T) {
	data := []byte(`
version: 1
review:
  gate:
    fail_on: error
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Review.Gate.FailOn != "error" {
		t.Errorf("Gate.FailOn = %q, want error", cfg.Review.Gate.FailOn)
	}
	if cfg.Review.Team.Max != 5 || cfg.Review.Team.Min != 1 {
		t.Errorf("Team = %+v, want defaults {1 5}", cfg.Review.Team)
	}
	if len(cfg.Review.SkipClasses) != 2 {
		t.Errorf("SkipClasses = %v, want the 2 defaults", cfg.Review.SkipClasses)
	}
}

func TestLoadRejectsUnknownKey(t *testing.T) {
	data := []byte("version: 1\nbogus_field: true\n")
	if _, err := Load(data); err == nil {
		t.Fatalf("Load accepted an unknown top-level key")
	}
}

func TestLoadRejectsBadVersion(t *testing.T) {
	if _, err := Load([]byte("version: 2\n")); err == nil {
		t.Fatalf("Load accepted version: 2")
	}
}

func TestLoadModelsAndPersonas(t *testing.T) {
	data := []byte(`
version: 1
models:
  review:
    endpoint: "http://spark:8000/v1"
    model: "qwen3-32b"
personas:
  security:
    priority: 20
`)
	cfg, err := Load(data)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Models["review"].Model != "qwen3-32b" {
		t.Errorf("Models = %+v", cfg.Models)
	}
	if cfg.Personas["security"].Priority == nil || *cfg.Personas["security"].Priority != 20 {
		t.Errorf("Personas[security] = %+v", cfg.Personas["security"])
	}
}
