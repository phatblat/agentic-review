// Package infer is the OpenAI-compatible /v1/chat/completions client:
// structured output via response_format json_schema, tool calling with
// the loop owned by the Go harness, and record/replay for deterministic
// tests.
package infer

import "encoding/json"

// Message is one chat-completions message.
type Message struct {
	Role       string     `json:"role"` // "system" | "user" | "assistant" | "tool"
	Content    string     `json:"content,omitempty"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"` // set when Role == "tool"
}

// Tool describes one function-calling tool.
type Tool struct {
	Type     string       `json:"type"` // "function"
	Function ToolFunction `json:"function"`
}

// ToolFunction is a tool's callable signature.
type ToolFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema object
}

// ToolCall is one model-requested tool invocation.
type ToolCall struct {
	ID       string           `json:"id"`
	Type     string           `json:"type"` // "function"
	Function ToolCallFunction `json:"function"`
}

// ToolCallFunction is a tool call's requested function and arguments.
type ToolCallFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"` // JSON-encoded argument object
}

// ResponseFormat requests OpenAI-shaped structured output.
type ResponseFormat struct {
	Type       string         `json:"type"` // "json_schema"
	JSONSchema JSONSchemaSpec `json:"json_schema"`
}

// JSONSchemaSpec names and pins the schema for guided decoding.
type JSONSchemaSpec struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
	Strict bool            `json:"strict"`
}

// Request is one /v1/chat/completions request body. Temperature, TopP, and
// Seed are pinned by Complete on every call (spec §10.1 determinism
// decision) — callers do not set them.
//
// ResponseFormat is the sole structured-output field (spec §10.1): the
// request body is strictly OpenAI-compatible, with no vLLM-specific
// guided_json extra sent alongside it.
type Request struct {
	Model          string          `json:"model"`
	Messages       []Message       `json:"messages"`
	Temperature    float64         `json:"temperature"`
	TopP           float64         `json:"top_p"`
	Seed           int             `json:"seed"`
	MaxTokens      int             `json:"max_tokens"`
	Tools          []Tool          `json:"tools,omitempty"`
	ToolChoice     string          `json:"tool_choice,omitempty"`
	ResponseFormat *ResponseFormat `json:"response_format,omitempty"`
}

// Response is a /v1/chat/completions response.
type Response struct {
	Choices []Choice `json:"choices"`
	Usage   Usage    `json:"usage"`
}

// Choice is one completion choice; agentic-review only ever requests one.
type Choice struct {
	Message      Message `json:"message"`
	FinishReason string  `json:"finish_reason"`
}

// Truncated reports whether resp was cut off at the request's max_tokens
// ceiling instead of completing on its own.
//
// Every structured-output caller needs this before it interprets a decode
// failure: a truncated response is never valid JSON, but its cause is a
// budget too small for the model, not a model that misunderstood the
// schema. Retrying a truncation with the same ceiling reproduces it
// exactly, and re-feeding the fragment as conversation only spends the
// next attempt's context on it.
func Truncated(resp *Response) bool {
	return len(resp.Choices) > 0 && resp.Choices[0].FinishReason == "length"
}

// TruncationRetryPrompt is the corrective turn every structured-output
// caller sends after a truncated response: the budget is fixed, so the
// only thing that can change is the length of what the model tries to
// say.
const TruncationRetryPrompt = "Your previous response was cut off before it finished — it exceeded the token budget for this turn. " +
	"Respond again with a single JSON object matching the schema exactly, nothing else, and keep every free-text field to one short sentence."

// PromptTokensDetails is the nested prompt-token breakdown Casper (and
// other OpenAI-compatible servers with prompt caching) report alongside
// the flat prompt_tokens count.
type PromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

// Usage is the token accounting the runtime reads for budget bookkeeping
// and cache-hit observability (spec §13.3's budget.json usage block).
type Usage struct {
	PromptTokens        int                 `json:"prompt_tokens"`
	PromptTokensDetails PromptTokensDetails `json:"prompt_tokens_details"`
	CompletionTokens    int                 `json:"completion_tokens"`
	TotalTokens         int                 `json:"total_tokens"`
}
