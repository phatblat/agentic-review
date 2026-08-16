// Package localconfig loads config.yaml and the repo-local personas/ tree
// from a checked-out repository directory (spec §3.1, §12.4: "config
// loads from the PR head" — the runner's checkout already put the head
// ref on disk, so this reads the filesystem rather than the GitHub API).
// Shared by `agentic-review review`, `plan`, and `validate` so all three
// use exactly the same load path (spec §5.1).
package localconfig

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/persona"
)

// Dir is the repo-relative directory holding config.yaml and the
// repo-local personas/ tree.
const Dir = ".github/agentic-review"

// Load reads config.yaml and every repo-local persona definition from
// root/.github/agentic-review, resolves them against the builtin roster,
// and returns the resolved registry plus the merged builtin+repo-local
// prompt map. root may be the repo root (every test fixture and the
// `review`/`plan`/`validate` CLI flags use this convention) or the
// .github/agentic-review directory itself (action.yml's config_path
// input convention, default "`.github/agentic-review`"); resolveRoot
// normalizes the latter to the former.
func Load(root string) (persona.Registry, map[string]string, *config.Config, error) {
	root = resolveRoot(root)
	configPath := filepath.Join(root, Dir, "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil && !os.IsNotExist(err) {
		return nil, nil, nil, fmt.Errorf("read %s: %w", configPath, err)
	}
	cfg, err := config.Load(data)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", configPath, err)
	}

	builtins, builtinPrompts, err := persona.Builtin()
	if err != nil {
		return nil, nil, nil, err
	}

	personasDir := filepath.Join(root, Dir, "personas")
	var repoLocal []persona.Definition
	repoLocalPrompts := map[string]string{}
	if info, statErr := os.Stat(personasDir); statErr == nil && info.IsDir() {
		repoLocal, repoLocalPrompts, err = persona.LoadDir(os.DirFS(root), filepath.ToSlash(filepath.Join(Dir, "personas")))
		if err != nil {
			return nil, nil, nil, err
		}
	}

	reg, err := persona.Resolve(builtins, repoLocal, cfg)
	if err != nil {
		return nil, nil, nil, err
	}
	prompts := persona.MergePrompts(builtinPrompts, repoLocalPrompts)
	return reg, prompts, cfg, nil
}

// resolveRoot normalizes root so Load always joins Dir onto a true repo
// root. A root already ending in Dir's two path segments (the
// .github/agentic-review directory itself) is walked up two levels; any
// other value — including "." and every test fixture root — is
// returned unchanged.
func resolveRoot(root string) string {
	clean := filepath.Clean(root)
	if filepath.Base(clean) == "agentic-review" && filepath.Base(filepath.Dir(clean)) == ".github" {
		return filepath.Dir(filepath.Dir(clean))
	}
	return root
}
