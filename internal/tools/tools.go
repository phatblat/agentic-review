// Package tools implements the two runtime-mediated capabilities a persona
// may call: read_file and osv_lookup. The harness owns the loop — the
// model never gets direct filesystem or network access, only these
// budgeted, JSON-in/JSON-out functions.
package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"path"
	"strings"

	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/osv"
)

const (
	maxLinesPerCall = 400
	maxBytesPerCall = 32 << 10
)

var readFileTool = infer.Tool{
	Type: "function",
	Function: infer.ToolFunction{
		Name:        "read_file",
		Description: "Read a slice of a file's content at the reviewed commit (head_sha).",
		Parameters: json.RawMessage(`{
			"type": "object",
			"additionalProperties": false,
			"required": ["path"],
			"properties": {
				"path": {"type": "string", "description": "repo-relative file path"},
				"start_line": {"type": "integer", "minimum": 1, "description": "1-based, inclusive; defaults to 1"},
				"end_line": {"type": "integer", "minimum": 1, "description": "1-based, inclusive; defaults to end of file"}
			}
		}`),
	},
}

var osvLookupTool = infer.Tool{
	Type: "function",
	Function: infer.ToolFunction{
		Name:        "osv_lookup",
		Description: "Look up known OSV advisories for a package version.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"additionalProperties": false,
			"required": ["ecosystem", "name", "version"],
			"properties": {
				"ecosystem": {"type": "string", "enum": ["cargo", "npm", "go", "deno"]},
				"name": {"type": "string"},
				"version": {"type": "string"}
			}
		}`),
	},
}

// Definitions returns the infer.Tool declarations for the given tool names
// (a persona's inputs.tools), in a stable order; unrecognised names are
// silently omitted — persona.ParseDefinition already rejects them at load
// time.
func Definitions(names []string) []infer.Tool {
	var out []infer.Tool
	for _, name := range names {
		switch name {
		case "read_file":
			out = append(out, readFileTool)
		case "osv_lookup":
			out = append(out, osvLookupTool)
		}
	}
	return out
}

// Registry executes tool calls for one persona's turn, decrementing a
// shared call budget.
type Registry struct {
	store     *gh.ContentStore
	headSHA   string
	osvClient *osv.Client
	remaining int
}

// NewRegistry builds a Registry with maxCalls of tool-call budget.
// osvClient may be nil if the persona's inputs.tools never includes
// osv_lookup.
func NewRegistry(store *gh.ContentStore, headSHA string, osvClient *osv.Client, maxCalls int) *Registry {
	return &Registry{store: store, headSHA: headSHA, osvClient: osvClient, remaining: maxCalls}
}

// Remaining reports the tool-call budget left.
func (r *Registry) Remaining() int { return r.remaining }

// Call dispatches one tool call by name, decrementing the remaining
// budget. Every result — success or failure — is a JSON object string;
// Call never panics or returns a Go error for a bad argument or lookup
// failure, only the {"error": "..."} shape the model sees as its tool
// result.
func (r *Registry) Call(ctx context.Context, name string, argumentsJSON string) string {
	if r.remaining <= 0 {
		return toolError("tool call budget exhausted")
	}
	r.remaining--

	switch name {
	case "read_file":
		return r.readFile(ctx, argumentsJSON)
	case "osv_lookup":
		return r.osvLookup(ctx, argumentsJSON)
	default:
		return toolError(fmt.Sprintf("unknown tool %q", name))
	}
}

type readFileArgs struct {
	Path      string `json:"path"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
}

func (r *Registry) readFile(ctx context.Context, argumentsJSON string) string {
	var args readFileArgs
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return toolError(fmt.Sprintf("invalid arguments: %v", err))
	}

	clean := path.Clean(args.Path)
	if clean != args.Path || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return toolError(fmt.Sprintf("invalid path %q", args.Path))
	}

	data, err := r.store.Get(ctx, clean, r.headSHA)
	if err != nil {
		return toolError(err.Error())
	}

	lines := strings.Split(string(data), "\n")
	start := args.StartLine
	if start < 1 {
		start = 1
	}
	if start > len(lines) {
		return toolError(fmt.Sprintf("start_line %d is past end of file (%d lines)", start, len(lines)))
	}
	end := args.EndLine
	if end < start || end > len(lines) {
		end = len(lines)
	}
	if end-start+1 > maxLinesPerCall {
		end = start + maxLinesPerCall - 1
	}

	var b strings.Builder
	total := 0
	lastLine := start - 1
	for i := start; i <= end; i++ {
		line := fmt.Sprintf("%d\t%s\n", i, lines[i-1])
		if total+len(line) > maxBytesPerCall {
			break
		}
		b.WriteString(line)
		total += len(line)
		lastLine = i
	}

	out, err := json.Marshal(map[string]any{
		"path": clean, "start_line": start, "end_line": lastLine, "content": b.String(),
	})
	if err != nil {
		return toolError(err.Error())
	}
	return string(out)
}

type osvLookupArgs struct {
	Ecosystem string `json:"ecosystem"`
	Name      string `json:"name"`
	Version   string `json:"version"`
}

func (r *Registry) osvLookup(ctx context.Context, argumentsJSON string) string {
	var args osvLookupArgs
	if err := json.Unmarshal([]byte(argumentsJSON), &args); err != nil {
		return toolError(fmt.Sprintf("invalid arguments: %v", err))
	}
	if r.osvClient == nil {
		return toolError("osv_lookup is not available for this persona")
	}
	vulns, err := r.osvClient.Lookup(ctx, args.Ecosystem, args.Name, args.Version)
	if err != nil {
		return toolError(err.Error())
	}
	out, err := json.Marshal(map[string]any{"vulns": vulns})
	if err != nil {
		return toolError(err.Error())
	}
	return string(out)
}

func toolError(msg string) string {
	out, err := json.Marshal(map[string]string{"error": msg})
	if err != nil {
		// json.Marshal on a map[string]string cannot fail; this is
		// unreachable, but never return an unparseable tool result.
		return `{"error":"internal error formatting tool error"}`
	}
	return string(out)
}
