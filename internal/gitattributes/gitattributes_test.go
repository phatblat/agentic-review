package gitattributes

import "testing"

func TestGenerated(t *testing.T) {
	data := []byte(`# comment
*.pb.go linguist-generated
dist/** -linguist-generated
generated/api.go linguist-generated=false
`)
	s := Parse(data)

	tests := []struct {
		path    string
		wantVal bool
		wantOK  bool
	}{
		{"pkg/api/foo.pb.go", true, true},
		{"dist/bundle.js", false, true},
		{"generated/api.go", false, true},
		{"src/main.go", false, false},
	}
	for _, tc := range tests {
		val, ok := s.Generated(tc.path)
		if val != tc.wantVal || ok != tc.wantOK {
			t.Errorf("Generated(%q) = (%v, %v), want (%v, %v)", tc.path, val, ok, tc.wantVal, tc.wantOK)
		}
	}
}

func TestGeneratedLaterRuleOverrides(t *testing.T) {
	data := []byte(`vendor/** linguist-generated
vendor/keep.go -linguist-generated
`)
	s := Parse(data)
	if val, ok := s.Generated("vendor/keep.go"); !ok || val {
		t.Errorf("Generated(vendor/keep.go) = (%v,%v), want (false,true) — later rule must win", val, ok)
	}
	if val, ok := s.Generated("vendor/other.go"); !ok || !val {
		t.Errorf("Generated(vendor/other.go) = (%v,%v), want (true,true)", val, ok)
	}
}

func TestGeneratedNilSet(t *testing.T) {
	var s *Set
	if val, ok := s.Generated("anything.go"); val || ok {
		t.Errorf("nil Set.Generated = (%v,%v), want (false,false)", val, ok)
	}
}
