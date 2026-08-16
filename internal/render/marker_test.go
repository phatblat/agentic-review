package render

import (
	"strings"
	"testing"
)

func TestRenderSummaryFixedKeyOrder(t *testing.T) {
	got := Render(Marker{Kind: "summary", Fields: map[string]string{
		"status": "findings", "run": "17283", "history": "[]",
	}})
	want := `<!-- agentic-review/1 kind=summary run=17283 status=findings history=%5B%5D -->`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderFindingFixedKeyOrder(t *testing.T) {
	got := Render(Marker{Kind: "finding", Fields: map[string]string{
		"sev": "blocker", "persona": "security", "seq": "1", "fp": "sha256:abc", "run": "17283",
	}})
	want := `<!-- agentic-review/1 kind=finding fp=sha256%3Aabc run=17283 seq=1 persona=security sev=blocker -->`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderAckFixedKeyOrder(t *testing.T) {
	got := Render(Marker{Kind: "ack", Fields: map[string]string{"run": "17283", "in-reply-to": "555"}})
	want := `<!-- agentic-review/1 kind=ack run=17283 in-reply-to=555 -->`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestRenderOmitsAbsentOptionalField(t *testing.T) {
	got := Render(Marker{Kind: "ack", Fields: map[string]string{"run": "17283"}})
	want := `<!-- agentic-review/1 kind=ack run=17283 -->`
	if got != want {
		t.Errorf("got  %q\nwant %q", got, want)
	}
}

func TestParseRoundTrip(t *testing.T) {
	original := Marker{Kind: "finding", Fields: map[string]string{
		"fp": "sha256:abc", "run": "17283", "seq": "1", "persona": "security", "sev": "blocker",
	}}
	body := Render(original) + "\n\nsome comment body text\nsecond line"

	got, ok := Parse(body)
	if !ok {
		t.Fatalf("Parse failed on a just-rendered marker")
	}
	if got.Version != MarkerVersion || got.Kind != "finding" {
		t.Errorf("got version/kind = %d/%q, want %d/finding", got.Version, got.Kind, MarkerVersion)
	}
	for k, v := range original.Fields {
		if got.Fields[k] != v {
			t.Errorf("Fields[%q] = %q, want %q", k, got.Fields[k], v)
		}
	}
}

func TestParseOnlyReadsFirstLine(t *testing.T) {
	marker := Render(Marker{Kind: "summary", Fields: map[string]string{"run": "1", "status": "ok", "history": ""}})
	body := marker + "\n" + Render(Marker{Kind: "ack", Fields: map[string]string{"run": "2"}})
	got, ok := Parse(body)
	if !ok || got.Kind != "summary" {
		t.Fatalf("Parse read past the first line: got %+v, ok=%v", got, ok)
	}
}

func TestParseRejectsWrongPrefix(t *testing.T) {
	if _, ok := Parse("<!-- some-other-tool/1 kind=summary -->"); ok {
		t.Errorf("Parse accepted a marker with the wrong tool prefix")
	}
}

func TestParseRejectsMissingSuffix(t *testing.T) {
	if _, ok := Parse("<!-- agentic-review/1 kind=summary run=1"); ok {
		t.Errorf("Parse accepted a marker missing its closing -->")
	}
}

func TestParseRejectsUnparseableVersion(t *testing.T) {
	if _, ok := Parse("<!-- agentic-review/vX kind=summary -->"); ok {
		t.Errorf("Parse accepted a non-numeric version")
	}
}

func TestParseRejectsFieldWithoutEquals(t *testing.T) {
	if _, ok := Parse("<!-- agentic-review/1 kind=summary bogus -->"); ok {
		t.Errorf("Parse accepted a field token with no '='")
	}
}

func TestParseRejectsMissingKind(t *testing.T) {
	if _, ok := Parse("<!-- agentic-review/1 run=1 -->"); ok {
		t.Errorf("Parse accepted a marker with no kind field")
	}
}

func TestParsePreservesUnknownKeys(t *testing.T) {
	got, ok := Parse("<!-- agentic-review/1 kind=summary run=1 future-field=xyz -->")
	if !ok {
		t.Fatalf("Parse failed on a marker with an unrecognised field")
	}
	if got.Fields["future-field"] != "xyz" {
		t.Errorf("Fields = %+v, want future-field preserved", got.Fields)
	}
}

func TestParseDecodesQueryEscapedValues(t *testing.T) {
	got, ok := Parse("<!-- agentic-review/1 kind=finding fp=sha256%3Aabc -->")
	if !ok {
		t.Fatalf("Parse failed")
	}
	if got.Fields["fp"] != "sha256:abc" {
		t.Errorf("fp = %q, want sha256:abc (decoded)", got.Fields["fp"])
	}
}

func TestParseHandlesNoTrailingBody(t *testing.T) {
	got, ok := Parse("<!-- agentic-review/1 kind=ack run=1 -->")
	if !ok || got.Kind != "ack" {
		t.Fatalf("Parse failed on a marker with no trailing body: got %+v, ok=%v", got, ok)
	}
}

func TestMarkerVersionIsOne(t *testing.T) {
	if MarkerVersion != 1 {
		t.Errorf("MarkerVersion = %d, want 1", MarkerVersion)
	}
}

func TestRenderValuesAreQueryEscaped(t *testing.T) {
	got := Render(Marker{Kind: "summary", Fields: map[string]string{"run": "1", "status": "a b&c", "history": ""}})
	if !strings.Contains(got, "status=a+b%26c") {
		t.Errorf("got %q, want status value query-escaped", got)
	}
}
