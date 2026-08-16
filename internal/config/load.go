package config

import (
	"fmt"

	"github.com/goccy/go-yaml"
)

// Load strict-decodes config.yaml bytes and fills every unset field from
// Defaults(). Missing config.yaml (data of length 0) returns Defaults()
// itself — config.yaml is optional; every field defaults independently.
func Load(data []byte) (*Config, error) {
	if len(data) == 0 {
		return Defaults(), nil
	}

	var cfg Config
	if err := yaml.UnmarshalWithOptions(data, &cfg, yaml.Strict()); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if cfg.Version != 0 && cfg.Version != 1 {
		return nil, fmt.Errorf("config: version %d is not supported (want 1)", cfg.Version)
	}
	cfg.Version = 1

	def := Defaults()
	if cfg.Review.Team.Min == 0 && cfg.Review.Team.Max == 0 {
		cfg.Review.Team = def.Review.Team
	}
	if len(cfg.Review.SkipClasses) == 0 {
		cfg.Review.SkipClasses = def.Review.SkipClasses
	}
	if cfg.Review.Gate.FailOn == "" {
		cfg.Review.Gate = def.Review.Gate
	}
	if len(cfg.Review.Caps) == 0 {
		cfg.Review.Caps = def.Review.Caps
	}
	if cfg.Review.Verification.MaterialityFloor == "" {
		cfg.Review.Verification = def.Review.Verification
	}
	if len(cfg.DocsGlobs) == 0 {
		cfg.DocsGlobs = def.DocsGlobs
	}
	return &cfg, nil
}
