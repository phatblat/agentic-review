// Package goldentest is the shared golden-file comparison helper for
// internal/classes, internal/roster, and internal/runner's golden test
// suites (spec item 54): compare with go-cmp; `go test ./... -update`
// rewrites the golden file to the actual output instead of failing.
package goldentest

import (
	"encoding/json"
	"flag"
	"os"
	"testing"

	"github.com/google/go-cmp/cmp"
)

// Update is true when the test binary was invoked with -update.
var Update = flag.Bool("update", false, "rewrite golden files to match actual output")

// JSON compares got (marshaled with 2-space indent) against the JSON file
// at path. With -update, it writes got to path instead of comparing.
func JSON(t *testing.T, path string, got any) {
	t.Helper()
	gotData, err := json.MarshalIndent(got, "", "  ")
	if err != nil {
		t.Fatalf("goldentest: marshal actual output: %v", err)
	}
	gotData = append(gotData, '\n')

	if *Update {
		if err := os.WriteFile(path, gotData, 0o644); err != nil {
			t.Fatalf("goldentest: write %s: %v", path, err)
		}
		return
	}

	wantData, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("goldentest: read %s: %v (run with -update to create it)", path, err)
	}

	var want, gotVal any
	if err := json.Unmarshal(wantData, &want); err != nil {
		t.Fatalf("goldentest: unmarshal %s: %v", path, err)
	}
	if err := json.Unmarshal(gotData, &gotVal); err != nil {
		t.Fatalf("goldentest: unmarshal actual output: %v", err)
	}
	if diff := cmp.Diff(want, gotVal); diff != "" {
		t.Errorf("%s mismatch (-want +got):\n%s", path, diff)
	}
}
