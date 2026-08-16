package manifest

import (
	"reflect"
	"sort"
)

// asMap type-asserts v as a decoded JSON/TOML object.
func asMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// subMap returns m[key] as a map, or nil if m is nil, the key is absent, or
// the value isn't an object.
func subMap(m map[string]any, key string) map[string]any {
	if m == nil {
		return nil
	}
	v, ok := m[key]
	if !ok {
		return nil
	}
	mm, _ := asMap(v)
	return mm
}

// cloneMap makes a shallow copy of m so callers can delete keys from the
// copy without mutating the original.
func cloneMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// unionKeys returns the sorted union of every key across maps.
func unionKeys(maps ...map[string]any) []string {
	seen := map[string]bool{}
	var out []string
	for _, m := range maps {
		for k := range m {
			if !seen[k] {
				seen[k] = true
				out = append(out, k)
			}
		}
	}
	sort.Strings(out)
	return out
}

// diffDepSection compares a flat "name -> spec" dependency map (Cargo.toml
// [dependencies], package.json "dependencies", etc). A name present on only
// one side is a structural addition/removal, which is never version-only. A
// name present on both sides is version-only when its spec is a plain
// string (any change, including no change, is permitted) or a table whose
// only difference is a scalar "version" field; any other difference —
// including a type change, or a change to a non-version field of a
// table-form spec — is not version-only.
func diffDepSection(ecosystem string, base, head map[string]any) (ok bool, changes []Change) {
	if len(base) == 0 && len(head) == 0 {
		return true, nil
	}
	for _, name := range unionKeys(base, head) {
		bv, bok := base[name]
		hv, hok := head[name]
		if bok != hok {
			return false, nil // dependency added or removed
		}
		switch bt := bv.(type) {
		case string:
			ht, isStr := hv.(string)
			if !isStr {
				return false, nil
			}
			if bt != ht {
				changes = append(changes, Change{Ecosystem: ecosystem, Name: name, From: bt, To: ht})
			}
		case map[string]any:
			ht, isMap := hv.(map[string]any)
			if !isMap {
				return false, nil
			}
			bCopy, hCopy := cloneMap(bt), cloneMap(ht)
			bVer, _ := bCopy["version"].(string)
			hVer, _ := hCopy["version"].(string)
			delete(bCopy, "version")
			delete(hCopy, "version")
			if !reflect.DeepEqual(bCopy, hCopy) {
				return false, nil // a non-version field of the table differs
			}
			if bVer != hVer {
				changes = append(changes, Change{Ecosystem: ecosystem, Name: name, From: bVer, To: hVer})
			}
		default:
			if !reflect.DeepEqual(bv, hv) {
				return false, nil
			}
		}
	}
	return true, changes
}
