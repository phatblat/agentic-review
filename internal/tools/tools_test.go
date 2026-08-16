package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/osv"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func testStore(t *testing.T, headContent map[string]string) (*gh.ContentStore, string) {
	t.Helper()
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "pr.json"), `{"number":1,"head_sha":"headsha"}`)
	for path, content := range headContent {
		writeFile(t, filepath.Join(dir, "head", path), content)
	}
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	store := gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"})
	return store, "headsha"
}

func TestReadFileBasic(t *testing.T) {
	store, headSHA := testStore(t, map[string]string{"main.go": "line1\nline2\nline3\n"})
	reg := NewRegistry(store, headSHA, nil, 5)

	result := reg.Call(context.Background(), "read_file", `{"path":"main.go"}`)
	var out map[string]any
	if err := json.Unmarshal([]byte(result), &out); err != nil {
		t.Fatalf("unmarshal result: %v (%s)", err, result)
	}
	content, _ := out["content"].(string)
	if !strings.Contains(content, "1\tline1") || !strings.Contains(content, "3\tline3") {
		t.Errorf("content = %q", content)
	}
}

func TestReadFileLineRange(t *testing.T) {
	store, headSHA := testStore(t, map[string]string{"f.go": "a\nb\nc\nd\ne\n"})
	reg := NewRegistry(store, headSHA, nil, 5)

	result := reg.Call(context.Background(), "read_file", `{"path":"f.go","start_line":2,"end_line":3}`)
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	content, _ := out["content"].(string)
	if strings.Contains(content, "1\ta") || !strings.Contains(content, "2\tb") || !strings.Contains(content, "3\tc") || strings.Contains(content, "4\td") {
		t.Errorf("content = %q, want only lines 2-3", content)
	}
}

func TestReadFileRejectsPathTraversal(t *testing.T) {
	store, headSHA := testStore(t, nil)
	reg := NewRegistry(store, headSHA, nil, 5)

	for _, p := range []string{"../etc/passwd", "/etc/passwd", "a/../../b", ".."} {
		result := reg.Call(context.Background(), "read_file", `{"path":"`+p+`"}`)
		var out map[string]string
		if err := json.Unmarshal([]byte(result), &out); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if out["error"] == "" {
			t.Errorf("path %q was not rejected: %s", p, result)
		}
	}
}

func TestReadFileMissingReturnsError(t *testing.T) {
	store, headSHA := testStore(t, nil)
	reg := NewRegistry(store, headSHA, nil, 5)

	result := reg.Call(context.Background(), "read_file", `{"path":"missing.go"}`)
	var out map[string]string
	_ = json.Unmarshal([]byte(result), &out)
	if out["error"] == "" {
		t.Errorf("expected an error for a missing file, got %s", result)
	}
}

func TestReadFileCapsAt400Lines(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		sb.WriteString("x\n")
	}
	store, headSHA := testStore(t, map[string]string{"big.go": sb.String()})
	reg := NewRegistry(store, headSHA, nil, 5)

	result := reg.Call(context.Background(), "read_file", `{"path":"big.go"}`)
	var out map[string]any
	_ = json.Unmarshal([]byte(result), &out)
	content, _ := out["content"].(string)
	lineCount := strings.Count(content, "\n")
	if lineCount > maxLinesPerCall {
		t.Errorf("lineCount = %d, want <= %d", lineCount, maxLinesPerCall)
	}
	if endLine, _ := out["end_line"].(float64); endLine != float64(maxLinesPerCall) {
		t.Errorf("end_line = %v, want %d", out["end_line"], maxLinesPerCall)
	}
}

func TestOsvLookupNoClientConfigured(t *testing.T) {
	store, headSHA := testStore(t, nil)
	reg := NewRegistry(store, headSHA, nil, 5)
	result := reg.Call(context.Background(), "osv_lookup", `{"ecosystem":"npm","name":"x","version":"1.0.0"}`)
	var out map[string]string
	_ = json.Unmarshal([]byte(result), &out)
	if out["error"] == "" {
		t.Errorf("expected an error with no osv client, got %s", result)
	}
}

func TestOsvLookupSuccess(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/querybatch", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"vulns":[{"id":"OSV-1"}]}]}`))
	})
	mux.HandleFunc("/v1/vulns/OSV-1", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"id":"OSV-1","summary":"x"}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	store, headSHA := testStore(t, nil)
	reg := NewRegistry(store, headSHA, osv.NewClientWithBaseURL(srv.URL), 5)
	result := reg.Call(context.Background(), "osv_lookup", `{"ecosystem":"npm","name":"x","version":"1.0.0"}`)
	if !strings.Contains(result, "OSV-1") {
		t.Errorf("result = %s, want it to contain OSV-1", result)
	}
}

func TestCallBudgetExhaustion(t *testing.T) {
	store, headSHA := testStore(t, map[string]string{"f.go": "a\n"})
	reg := NewRegistry(store, headSHA, nil, 2)

	for i := 0; i < 2; i++ {
		result := reg.Call(context.Background(), "read_file", `{"path":"f.go"}`)
		if strings.Contains(result, "budget exhausted") {
			t.Fatalf("call %d exhausted early: %s", i, result)
		}
	}
	result := reg.Call(context.Background(), "read_file", `{"path":"f.go"}`)
	var out map[string]string
	_ = json.Unmarshal([]byte(result), &out)
	if out["error"] != "tool call budget exhausted" {
		t.Errorf("error = %q, want \"tool call budget exhausted\"", out["error"])
	}
	if reg.Remaining() != 0 {
		t.Errorf("Remaining() = %d, want 0", reg.Remaining())
	}
}

func TestCallUnknownTool(t *testing.T) {
	store, headSHA := testStore(t, nil)
	reg := NewRegistry(store, headSHA, nil, 5)
	result := reg.Call(context.Background(), "delete_everything", `{}`)
	var out map[string]string
	_ = json.Unmarshal([]byte(result), &out)
	if out["error"] == "" {
		t.Errorf("expected an error for an unknown tool, got %s", result)
	}
}

func TestDefinitions(t *testing.T) {
	defs := Definitions([]string{"read_file", "osv_lookup", "unrecognized"})
	if len(defs) != 2 {
		t.Fatalf("Definitions = %+v, want 2 entries", defs)
	}
	if defs[0].Function.Name != "read_file" || defs[1].Function.Name != "osv_lookup" {
		t.Errorf("Definitions order = %s, %s", defs[0].Function.Name, defs[1].Function.Name)
	}
}
