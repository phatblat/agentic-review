package manifest

import (
	"encoding/json"
	"fmt"
	"reflect"
)

// npmParser handles package.json (npm/pnpm/yarn/bun share the manifest
// format; their lockfiles are matched by path alone, never parsed).
// Version-bearing sections: dependencies, devDependencies,
// peerDependencies, optionalDependencies, and the package's own version.
type npmParser struct{}

func (npmParser) Match(p string) bool { return baseName(p) == "package.json" }

func (npmParser) VersionOnly(base, head []byte) (bool, []Change, error) {
	var b, h map[string]any
	if err := json.Unmarshal(base, &b); err != nil {
		return false, nil, fmt.Errorf("manifest: parse base package.json: %w", err)
	}
	if err := json.Unmarshal(head, &h); err != nil {
		return false, nil, fmt.Errorf("manifest: parse head package.json: %w", err)
	}

	delete(b, "version")
	delete(h, "version")

	ok := true
	var changes []Change
	for _, section := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		sok, schanges := diffDepSection("npm", subMap(b, section), subMap(h, section))
		ok = ok && sok
		changes = append(changes, schanges...)
		delete(b, section)
		delete(h, section)
	}

	if !reflect.DeepEqual(b, h) {
		return false, changes, nil
	}
	return ok, changes, nil
}
