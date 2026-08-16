package manifest

import "testing"

func TestFor(t *testing.T) {
	cases := map[string]bool{
		"Cargo.toml":        true,
		"sub/Cargo.toml":    true,
		"package.json":      true,
		"deno.json":         true,
		"deno.jsonc":        true,
		"go.mod":            true,
		"Cargo.lock":        false,
		"random.toml":       false,
		"package-lock.json": false,
	}
	for path, want := range cases {
		got := For(path) != nil
		if got != want {
			t.Errorf("For(%q) matched = %v, want %v", path, got, want)
		}
	}
}

func TestCargoVersionOnlyBump(t *testing.T) {
	base := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
openssl = "1.0.2"
serde = { version = "1.0.100", features = ["derive"] }
`
	head := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
openssl = "3.2.1"
serde = { version = "1.0.200", features = ["derive"] }
`
	ok, changes, err := cargoParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if !ok {
		t.Fatalf("VersionOnly ok = false, want true")
	}
	if len(changes) != 2 {
		t.Fatalf("changes = %+v, want 2 entries", changes)
	}
}

func TestCargoNewDependencyIsNotVersionOnly(t *testing.T) {
	base := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
openssl = "1.0.2"
`
	head := `[package]
name = "demo"
version = "0.1.0"

[dependencies]
openssl = "1.0.2"
serde = "1"
`
	ok, _, err := cargoParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if ok {
		t.Fatalf("VersionOnly ok = true, want false for a new dependency")
	}
}

func TestCargoFeatureChangeIsNotVersionOnly(t *testing.T) {
	base := `[dependencies]
serde = { version = "1.0.100", features = ["derive"] }
`
	head := `[dependencies]
serde = { version = "1.0.100", features = ["derive", "rc"] }
`
	ok, _, err := cargoParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if ok {
		t.Fatalf("VersionOnly ok = true, want false for a features-list change")
	}
}

func TestNpmVersionOnly(t *testing.T) {
	base := `{"name":"demo","version":"1.0.0","dependencies":{"lodash":"^4.17.20"}}`
	head := `{"name":"demo","version":"1.0.1","dependencies":{"lodash":"^4.17.21"}}`
	ok, changes, err := npmParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if !ok || len(changes) != 1 {
		t.Fatalf("ok=%v changes=%+v, want ok=true and 1 change", ok, changes)
	}
}

func TestNpmScriptChangeIsNotVersionOnly(t *testing.T) {
	base := `{"name":"demo","version":"1.0.0","scripts":{"build":"tsc"}}`
	head := `{"name":"demo","version":"1.0.0","scripts":{"build":"tsc -p ."}}`
	ok, _, err := npmParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if ok {
		t.Fatalf("VersionOnly ok = true, want false for a scripts change")
	}
}

func TestDenoCommentOnlyIsVersionOnly(t *testing.T) {
	base := `{
  // demo project
  "imports": {
    "@std/testing": "jsr:@std/testing@1.0.0"
  }
}`
	head := `{
  // demo project, updated comment
  "imports": {
    "@std/testing": "jsr:@std/testing@1.0.0"
  }
}`
	ok, changes, err := denoParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if !ok || len(changes) != 0 {
		t.Fatalf("ok=%v changes=%+v, want ok=true and no changes", ok, changes)
	}
}

func TestDenoImportVersionBump(t *testing.T) {
	base := `{"imports":{"react":"npm:react@18.2.0"}}`
	head := `{"imports":{"react":"npm:react@18.3.1"}}`
	ok, changes, err := denoParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if !ok || len(changes) != 1 || changes[0].From != "18.2.0" || changes[0].To != "18.3.1" {
		t.Fatalf("ok=%v changes=%+v, want one 18.2.0->18.3.1 change", ok, changes)
	}
}

func TestGoModRequireBump(t *testing.T) {
	base := `module example.com/demo

go 1.26

require (
	github.com/google/go-cmp v0.6.0
	golang.org/x/text v0.20.0
)
`
	head := `module example.com/demo

go 1.26

require (
	github.com/google/go-cmp v0.7.0
	golang.org/x/text v0.20.0
)
`
	ok, changes, err := goModParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if !ok || len(changes) != 1 {
		t.Fatalf("ok=%v changes=%+v, want ok=true and 1 change", ok, changes)
	}
	if changes[0].Name != "github.com/google/go-cmp" || changes[0].From != "v0.6.0" || changes[0].To != "v0.7.0" {
		t.Errorf("changes[0] = %+v, want go-cmp v0.6.0->v0.7.0", changes[0])
	}
}

func TestGoModNewRequireIsNotVersionOnly(t *testing.T) {
	base := `module example.com/demo

go 1.26
`
	head := `module example.com/demo

go 1.26

require github.com/google/go-cmp v0.7.0
`
	ok, _, err := goModParser{}.VersionOnly([]byte(base), []byte(head))
	if err != nil {
		t.Fatalf("VersionOnly error: %v", err)
	}
	if ok {
		t.Fatalf("VersionOnly ok = true, want false for a newly added require")
	}
}
