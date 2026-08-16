package manifest

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	"github.com/tailscale/hujson"
)

// denoParser handles deno.json / deno.jsonc. Version-bearing: only the
// version portion after the last '@' in each "imports" map value (e.g.
// "npm:react@18.2.0" or "npm:@scope/name@1.2.3"), plus the manifest's own
// "version" field.
type denoParser struct{}

func (denoParser) Match(p string) bool {
	b := baseName(p)
	return b == "deno.json" || b == "deno.jsonc"
}

func (denoParser) VersionOnly(base, head []byte) (bool, []Change, error) {
	bStd, err := hujson.Standardize(base)
	if err != nil {
		return false, nil, fmt.Errorf("manifest: standardize base deno.json(c): %w", err)
	}
	hStd, err := hujson.Standardize(head)
	if err != nil {
		return false, nil, fmt.Errorf("manifest: standardize head deno.json(c): %w", err)
	}

	var b, h map[string]any
	if err := json.Unmarshal(bStd, &b); err != nil {
		return false, nil, fmt.Errorf("manifest: parse base deno.json(c): %w", err)
	}
	if err := json.Unmarshal(hStd, &h); err != nil {
		return false, nil, fmt.Errorf("manifest: parse head deno.json(c): %w", err)
	}

	delete(b, "version")
	delete(h, "version")

	bImports, hImports := subMap(b, "imports"), subMap(h, "imports")
	delete(b, "imports")
	delete(h, "imports")

	ok := true
	var changes []Change
	for _, name := range unionKeys(bImports, hImports) {
		bv, bok := bImports[name]
		hv, hok := hImports[name]
		if bok != hok {
			ok = false
			continue
		}
		bs, bIsStr := bv.(string)
		hs, hIsStr := hv.(string)
		if !bIsStr || !hIsStr {
			ok = false
			continue
		}
		bPrefix, bVer := splitLastAt(bs)
		hPrefix, hVer := splitLastAt(hs)
		if bPrefix != hPrefix {
			ok = false
			continue
		}
		if bVer != hVer {
			changes = append(changes, Change{Ecosystem: "deno", Name: name, From: bVer, To: hVer})
		}
	}

	if !reflect.DeepEqual(b, h) {
		return false, changes, nil
	}
	return ok, changes, nil
}

// splitLastAt splits s at its last '@', so a scoped specifier like
// "npm:@scope/name@1.2.3" splits into "npm:@scope/name" and "1.2.3".
func splitLastAt(s string) (prefix, version string) {
	i := strings.LastIndex(s, "@")
	if i < 0 {
		return s, ""
	}
	return s[:i], s[i+1:]
}
