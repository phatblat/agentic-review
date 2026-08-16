package manifest

import (
	"fmt"
	"reflect"

	"github.com/pelletier/go-toml/v2"
)

// cargoParser handles Cargo.toml. Version-bearing sections:
// [dependencies], [dev-dependencies], [build-dependencies],
// [workspace.dependencies], [target.<cfg>.dependencies], and
// [package] version.
type cargoParser struct{}

func (cargoParser) Match(p string) bool { return baseName(p) == "Cargo.toml" }

func (cargoParser) VersionOnly(base, head []byte) (bool, []Change, error) {
	var b, h map[string]any
	if err := toml.Unmarshal(base, &b); err != nil {
		return false, nil, fmt.Errorf("manifest: parse base Cargo.toml: %w", err)
	}
	if err := toml.Unmarshal(head, &h); err != nil {
		return false, nil, fmt.Errorf("manifest: parse head Cargo.toml: %w", err)
	}

	if pkg := subMap(b, "package"); pkg != nil {
		delete(pkg, "version")
	}
	if pkg := subMap(h, "package"); pkg != nil {
		delete(pkg, "version")
	}

	ok := true
	var changes []Change

	for _, section := range []string{"dependencies", "dev-dependencies", "build-dependencies"} {
		sok, schanges := diffDepSection("cargo", subMap(b, section), subMap(h, section))
		ok = ok && sok
		changes = append(changes, schanges...)
		delete(b, section)
		delete(h, section)
	}

	{
		sok, schanges := diffDepSection("cargo", subMap(subMap(b, "workspace"), "dependencies"), subMap(subMap(h, "workspace"), "dependencies"))
		ok = ok && sok
		changes = append(changes, schanges...)
		if ws := subMap(b, "workspace"); ws != nil {
			delete(ws, "dependencies")
		}
		if ws := subMap(h, "workspace"); ws != nil {
			delete(ws, "dependencies")
		}
	}

	bTarget, hTarget := subMap(b, "target"), subMap(h, "target")
	for _, cfg := range unionKeys(bTarget, hTarget) {
		bCfg, hCfg := subMap(bTarget, cfg), subMap(hTarget, cfg)
		sok, schanges := diffDepSection("cargo", subMap(bCfg, "dependencies"), subMap(hCfg, "dependencies"))
		ok = ok && sok
		changes = append(changes, schanges...)
		if bCfg != nil {
			delete(bCfg, "dependencies")
		}
		if hCfg != nil {
			delete(hCfg, "dependencies")
		}
	}

	if !reflect.DeepEqual(b, h) {
		return false, changes, nil
	}
	return ok, changes, nil
}
