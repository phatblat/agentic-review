package verify

import (
	"testing"

	"github.com/phatblat/agentic-review/internal/schema"
)

func TestFingerprintDeterministic(t *testing.T) {
	p := schema.Payload{
		Category: "security",
		Claim:    "Token expiry check removed, allowing expired tokens to authenticate",
		Anchor:   schema.Anchor{Kind: schema.AnchorLine, Path: "src/auth/Token.RS"},
		Evidence: []schema.Evidence{{Source: "let claims = decode_unverified(&token)?;"}},
	}
	f1 := Fingerprint(p)
	f2 := Fingerprint(p)
	if f1 != f2 {
		t.Errorf("Fingerprint is not deterministic: %s != %s", f1, f2)
	}
	if len(f1) != len("sha256:")+64 || f1[:7] != "sha256:" {
		t.Errorf("Fingerprint = %q, want \"sha256:\" + 64 hex chars", f1)
	}
}

func TestFingerprintIgnoresEvidenceOrder(t *testing.T) {
	base := schema.Payload{Category: "security", Claim: "x", Anchor: schema.Anchor{Path: "a.go"}}
	p1 := base
	p1.Evidence = []schema.Evidence{{Source: "aaa"}, {Source: "bbb"}}
	p2 := base
	p2.Evidence = []schema.Evidence{{Source: "bbb"}, {Source: "aaa"}}
	if Fingerprint(p1) != Fingerprint(p2) {
		t.Errorf("Fingerprint depends on evidence emission order")
	}
}

func TestFingerprintIgnoresWhitespaceDifferences(t *testing.T) {
	base := schema.Payload{Category: "security", Claim: "x", Anchor: schema.Anchor{Path: "a.go"}}
	p1 := base
	p1.Evidence = []schema.Evidence{{Source: "let   x =  1;"}}
	p2 := base
	p2.Evidence = []schema.Evidence{{Source: "let x = 1;"}}
	if Fingerprint(p1) != Fingerprint(p2) {
		t.Errorf("Fingerprint is sensitive to whitespace run length")
	}
}

func TestFingerprintPathCaseSensitiveExceptExtension(t *testing.T) {
	base := schema.Payload{Category: "security", Claim: "x"}
	p1 := base
	p1.Anchor = schema.Anchor{Path: "src/Auth/Token.RS"}
	p2 := base
	p2.Anchor = schema.Anchor{Path: "src/Auth/Token.rs"}
	p3 := base
	p3.Anchor = schema.Anchor{Path: "src/auth/Token.rs"}
	if Fingerprint(p1) != Fingerprint(p2) {
		t.Errorf("Fingerprint should lowercase only the extension: RS vs rs differ")
	}
	if Fingerprint(p2) == Fingerprint(p3) {
		t.Errorf("Fingerprint should stay case-sensitive outside the extension: Auth vs auth must differ")
	}
}

func TestFingerprintClaimStemTruncatesTo12Words(t *testing.T) {
	base := schema.Payload{Category: "x", Anchor: schema.Anchor{Path: "a.go"}}
	p1 := base
	p1.Claim = "one two three four five six seven eight nine ten eleven twelve THIRTEEN"
	p2 := base
	p2.Claim = "one two three four five six seven eight nine ten eleven twelve DIFFERENT-WORD-HERE"
	if Fingerprint(p1) != Fingerprint(p2) {
		t.Errorf("Fingerprint should ignore words past the 12th")
	}
}

func TestFingerprintDiffersOnCategory(t *testing.T) {
	base := schema.Payload{Claim: "x", Anchor: schema.Anchor{Path: "a.go"}}
	p1 := base
	p1.Category = "security"
	p2 := base
	p2.Category = "correctness"
	if Fingerprint(p1) == Fingerprint(p2) {
		t.Errorf("Fingerprint should differ when category differs")
	}
}

func TestFingerprintBackslashNormalized(t *testing.T) {
	base := schema.Payload{Category: "x", Claim: "x"}
	p1 := base
	p1.Anchor = schema.Anchor{Path: `src\auth\token.rs`}
	p2 := base
	p2.Anchor = schema.Anchor{Path: "src/auth/token.rs"}
	if Fingerprint(p1) != Fingerprint(p2) {
		t.Errorf("Fingerprint should normalize backslashes to forward slashes")
	}
}
