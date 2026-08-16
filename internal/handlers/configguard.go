package handlers

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/schema"
)

// configPathPrefix is every review-config path's common root (spec §4's
// review-config class).
const configPathPrefix = ".github/agentic-review/"

// securityRelevantPersonas is the set a new prompt overlay is a blocker
// for (spec item 44).
var securityRelevantPersonas = map[string]bool{
	"security": true, "fork-guard": true, "config-guard": true,
	"verifier/injection": true, "verifier/groundedness": true,
}

// ConfigGuardInput bundles what builtin/config-guard needs. It cannot
// depend on internal/runner (internal/runner already depends on
// internal/handlers), so PR content and the system prompt are passed as
// plain fields rather than runner.PRContext.
type ConfigGuardInput struct {
	Client       infer.Client
	Cfg          *config.Config
	ReviewModel  *persona.Model
	SystemPrompt string
	Store        *gh.ContentStore
	Files        []gh.File
	// FileClasses is facts.Assemble's per-file classification (its second
	// return value). Review-config is "any path under
	// .github/agentic-review/** or the invoking workflow file" (spec §4)
	// — the latter can't be recognised from the path alone, so ConfigGuard
	// reuses the same classification facts.Assemble already computed
	// rather than re-deriving GITHUB_WORKFLOW_REF itself.
	FileClasses map[string]classes.Class
	BaseSHA     string
	HeadSHA     string
	PRTitle     string
	PRBody      string
}

// ConfigGuard implements builtin/config-guard (spec §12.4): a deterministic
// pass over every changed file under .github/agentic-review/** emits a
// blocker for each certain weakening fact; a model judgment pass over the
// same diff then evaluates intent. The model pass degrading (any error)
// only loses the judgment findings — the deterministic blockers, the
// security-critical half, are returned regardless.
func ConfigGuard(ctx context.Context, in ConfigGuardInput) ([]schema.Payload, error) {
	changed := changedConfigFiles(in.Files, in.FileClasses)
	if len(changed) == 0 {
		return nil, nil
	}

	var out []schema.Payload
	var diffText strings.Builder

	for _, path := range changed {
		base, baseErr := in.Store.Get(ctx, path, in.BaseSHA)
		head, headErr := in.Store.Get(ctx, path, in.HeadSHA)
		if baseErr != nil {
			base = nil
		}
		if headErr != nil {
			head = nil
		}

		fmt.Fprintf(&diffText, "--- %s (base)\n%s\n+++ %s (head)\n%s\n\n", path, base, path, head)

		switch {
		case path == configPathPrefix+"config.yaml":
			out = append(out, diffConfigYAML(path, base, head)...)
		case strings.HasPrefix(path, configPathPrefix+"personas/"):
			out = append(out, diffPersonaYAML(path, base, head)...)
		default:
			out = append(out, diffWorkflowPermissions(path, base, head)...)
		}
	}

	judged, err := judgeIntent(ctx, in, diffText.String())
	if err != nil {
		logx.Warn("config-guard: model judgment unavailable: %v", err)
	} else {
		out = append(out, judged...)
	}

	return out, nil
}

func changedConfigFiles(files []gh.File, fileClasses map[string]classes.Class) []string {
	var out []string
	for _, f := range files {
		if fileClasses[f.Path] == classes.ClassReviewConfig {
			out = append(out, f.Path)
		}
	}
	return out
}

func diffConfigYAML(path string, base, head []byte) []schema.Payload {
	var out []schema.Payload
	baseCfg, baseErr := config.Load(base)
	headCfg, headErr := config.Load(head)
	if baseErr != nil || headErr != nil {
		return out // unparseable; the model judgment pass still sees the raw diff
	}

	if severityRank(headCfg.Review.Gate.FailOn) > severityRank(baseCfg.Review.Gate.FailOn) {
		out = append(out, configBlocker(path, fmt.Sprintf("gate.fail_on weakened from %s to %s", baseCfg.Review.Gate.FailOn, headCfg.Review.Gate.FailOn)))
	}
	for _, class := range headCfg.Review.SkipClasses {
		if !containsStr(baseCfg.Review.SkipClasses, class) {
			out = append(out, configBlocker(path, fmt.Sprintf("skip_classes extended with %s", class)))
		}
	}
	for _, glob := range headCfg.DocsGlobs {
		if !containsStr(baseCfg.DocsGlobs, glob) {
			out = append(out, configBlocker(path, fmt.Sprintf("docs_globs extended with %s", glob)))
		}
	}

	for id, headOv := range headCfg.Personas {
		baseOv := baseCfg.Personas[id]
		if headOv.Enabled != nil && !*headOv.Enabled && (baseOv.Enabled == nil || *baseOv.Enabled) {
			out = append(out, configBlocker(path, fmt.Sprintf("persona %q disabled", id)))
		}
		if headOv.Budget != nil {
			var baseTokens, baseCalls int
			if baseOv.Budget != nil {
				baseTokens, baseCalls = baseOv.Budget.MaxTokens, baseOv.Budget.MaxToolCalls
			}
			if headOv.Budget.MaxTokens > baseTokens || headOv.Budget.MaxToolCalls > baseCalls {
				out = append(out, configBlocker(path, fmt.Sprintf("budget raised for persona %q", id)))
			}
		}
		if headOv.Overlay != "" && headOv.Overlay != baseOv.Overlay && securityRelevantPersonas[id] {
			out = append(out, configBlocker(path, fmt.Sprintf("prompt overlay added to security-relevant persona %q", id)))
		}
	}
	return out
}

func diffPersonaYAML(path string, base, head []byte) []schema.Payload {
	if len(head) == 0 || len(base) == 0 {
		return nil // file deleted or newly added: nothing to compare
	}
	headDef, err := persona.ParseDefinition(path, head)
	if err != nil {
		return nil // let the model judgment pass see the raw diff
	}
	baseDef, err := persona.ParseDefinition(path, base)
	if err != nil {
		return nil
	}

	var out []schema.Payload
	if baseDef.Activation.RequiredWhen != "" && headDef.Activation.RequiredWhen == "" {
		out = append(out, configBlocker(path, fmt.Sprintf("required_when removed from persona %q", headDef.ID)))
	}
	return out
}

// permissionsBlockRE matches a top-level "permissions:" key and every
// indented line beneath it — a plain-text approximation of the YAML block,
// deliberately not a full YAML parse (workflow permissions can be a bare
// string like "permissions: write-all" or a nested block; both are caught
// by this pattern since indented continuation lines are optional).
var permissionsBlockRE = regexp.MustCompile(`(?m)^permissions:.*(\n[ \t]+.*)*`)

func diffWorkflowPermissions(path string, base, head []byte) []schema.Payload {
	baseBlock := strings.TrimSpace(permissionsBlockRE.FindString(string(base)))
	headBlock := strings.TrimSpace(permissionsBlockRE.FindString(string(head)))
	if baseBlock == headBlock {
		return nil
	}
	return []schema.Payload{configBlocker(path, fmt.Sprintf("workflow permissions changed in %s", path))}
}

func configBlocker(path, title string) schema.Payload {
	return schema.Payload{
		Category:   "config",
		Severity:   "blocker",
		Title:      title,
		Claim:      title,
		Anchor:     schema.Anchor{Kind: schema.AnchorFile, Path: path},
		Confidence: 1,
	}
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func severityRank(s string) int {
	for i, sev := range schema.Severities {
		if sev == s {
			return i
		}
	}
	return -1
}

// judgeIntent is config-guard's agentic half: a review-capability call
// judging intent over the raw config diff text.
func judgeIntent(ctx context.Context, in ConfigGuardInput, diffText string) ([]schema.Payload, error) {
	if in.ReviewModel == nil {
		return nil, fmt.Errorf("config-guard has no model binding")
	}
	binding, ok := in.Cfg.Models[in.ReviewModel.Capability]
	if !ok {
		return nil, fmt.Errorf("capability %q has no models[] binding", in.ReviewModel.Capability)
	}

	schemaRaw, err := schema.Raw("findings.v1")
	if err != nil {
		return nil, err
	}

	userContent := fmt.Sprintf(
		"<untrusted-content source=\"pr-title\">\n%s\n</untrusted-content>\n\n<untrusted-content source=\"pr-body\">\n%s\n</untrusted-content>\n\n<untrusted-content source=\"config-diff\">\n%s\n</untrusted-content>",
		strings.ReplaceAll(in.PRTitle, "</untrusted-content>", `<\/untrusted-content>`),
		strings.ReplaceAll(in.PRBody, "</untrusted-content>", `<\/untrusted-content>`),
		strings.ReplaceAll(diffText, "</untrusted-content>", `<\/untrusted-content>`),
	)

	req := &infer.Request{
		Model: binding.Model,
		Messages: []infer.Message{
			{Role: "system", Content: in.SystemPrompt},
			{Role: "user", Content: userContent},
		},
		ResponseFormat: &infer.ResponseFormat{
			Type:       "json_schema",
			JSONSchema: infer.JSONSchemaSpec{Name: "findings_v1", Schema: schemaRaw, Strict: true},
		},
		GuidedJSON: schemaRaw,
	}
	resp, err := in.Client.Complete(ctx, binding.Endpoint, req)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, fmt.Errorf("response has no choices")
	}
	findingsResp, err := schema.DecodeFindings([]byte(resp.Choices[0].Message.Content))
	if err != nil {
		return nil, err
	}
	return findingsResp.Findings, nil
}
