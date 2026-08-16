package roster_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/goldentest"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
)

// TestGolden computes the roster for the real builtin persona set against
// each fixtures/roster/<case>/{facts.json, assessment.json|absent,
// config.yaml} directory and compares against want-roster.json (spec
// item 52, Verification item 4).
func TestGolden(t *testing.T) {
	roots, err := filepath.Glob("../../fixtures/roster/*")
	if err != nil {
		t.Fatalf("glob fixture roots: %v", err)
	}
	if len(roots) == 0 {
		t.Fatal("no fixtures found under fixtures/roster/*")
	}

	for _, root := range roots {
		root := root
		t.Run(filepath.Base(root), func(t *testing.T) {
			var f facts.Facts
			data, err := os.ReadFile(filepath.Join(root, "facts.json"))
			if err != nil {
				t.Fatalf("read facts.json: %v", err)
			}
			if err := json.Unmarshal(data, &f); err != nil {
				t.Fatalf("unmarshal facts.json: %v", err)
			}

			var assessment *schema.Assessment
			if data, err := os.ReadFile(filepath.Join(root, "assessment.json")); err == nil {
				assessment = &schema.Assessment{}
				if err := json.Unmarshal(data, assessment); err != nil {
					t.Fatalf("unmarshal assessment.json: %v", err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("read assessment.json: %v", err)
			}

			cfgData, err := os.ReadFile(filepath.Join(root, "config.yaml"))
			if err != nil {
				t.Fatalf("read config.yaml: %v", err)
			}
			cfg, err := config.Load(cfgData)
			if err != nil {
				t.Fatalf("config.Load: %v", err)
			}

			builtins, _, err := persona.Builtin()
			if err != nil {
				t.Fatalf("persona.Builtin: %v", err)
			}
			reg, err := persona.Resolve(builtins, nil, cfg)
			if err != nil {
				t.Fatalf("persona.Resolve: %v", err)
			}

			rst, err := roster.Compute(reg, &f, assessment, cfg)
			if err != nil {
				t.Fatalf("roster.Compute: %v", err)
			}

			goldentest.JSON(t, filepath.Join(root, "want-roster.json"), rst)
		})
	}
}
