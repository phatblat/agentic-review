package runner

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/phatblat/agentic-review/internal/classes"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/persona"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/verify"
)

// config-guard opts out of verification (required: false), but only for the
// facts it computes in code. This crosses the whole boundary: handler ->
// runMember -> stampEnvelope -> verify.Run. If any link dropped the
// model_authored marker, the model-judged finding would stay accepted
// without ever reaching a lens.
func TestRunTeamThenVerifyJudgesConfigGuardModelFindingAndSparesItsFact(t *testing.T) {
	const cfgPath = ".github/agentic-review/config.yaml"

	dir := t.TempDir()
	writeTeamFile(t, filepath.Join(dir, "pr.json"), `{"number":1,"base_sha":"basesha","head_sha":"headsha"}`)
	writeTeamFile(t, filepath.Join(dir, "base", cfgPath), "version: 1\nreview:\n  gate:\n    fail_on: warning\n")
	writeTeamFile(t, filepath.Join(dir, "head", cfgPath), "version: 1\nreview:\n  gate:\n    fail_on: error\n")
	fake, err := gh.LoadFake(dir)
	if err != nil {
		t.Fatalf("LoadFake: %v", err)
	}
	store := gh.NewContentStore(fake, gh.Repo{Owner: "acme", Name: "demo"})

	cfg := teamTestCfg(map[string]string{"review": "model-review", "verify": "model-verify"})
	reg := resolveTeam(t, []persona.Definition{
		{
			ID: "config-guard", Kind: persona.KindDeterministic, Summary: "x",
			Activation: persona.Activation{Always: true},
			Runtime:    &persona.Runtime{Handler: "builtin/config-guard"},
			Model:      &persona.Model{Capability: "review"},
			Budget:     persona.Budget{MaxTokens: 1000},
		},
		{
			ID: "verifier/groundedness", Kind: persona.KindVerifier, Lens: persona.LensGroundedness, Summary: "x",
			Model:  &persona.Model{Capability: "verify"},
			Budget: persona.Budget{MaxTokens: 1000},
		},
	}, cfg)
	rst, err := roster.Compute(reg, &facts.Facts{}, nil, cfg)
	if err != nil {
		t.Fatalf("roster.Compute: %v", err)
	}

	// f-0001 is the deterministic blocker (gate.fail_on weakened); f-0002 is
	// the model pass's finding, which the verifier rejects.
	cl := &keyedClient{responses: map[string]string{
		"model-review": `{"findings":[{"category":"config","severity":"warning","title":"suspicious intent","claim":"c",` +
			`"anchor":{"kind":"pr"},"evidence":[{"path":"` + cfgPath + `","start_line":1,"end_line":1,"source":"version: 1"}],"confidence":0.6}],"escalate":[]}`,
		"model-verify": `{"verdicts":[{"finding_id":"f-0002","result":"fail","reason":"not supported"}]}`,
	}}
	deps := TeamDeps{
		Client: cl, Cfg: cfg, Facts: &facts.Facts{}, Prompts: map[string]string{},
		Store: store, BaseSHA: "basesha", HeadSHA: "headsha",
		Files:       []gh.File{{Path: cfgPath}},
		FileClasses: map[string]classes.Class{cfgPath: classes.ClassReviewConfig},
	}

	found, _, err := RunTeam(context.Background(), deps, reg, rst)
	if err != nil {
		t.Fatalf("RunTeam: %v", err)
	}
	if len(found) != 2 {
		t.Fatalf("RunTeam returned %d findings, want the fact and the model finding: %+v", len(found), found)
	}

	out, _, err := verify.Run(context.Background(), found, verify.Env{
		Client: cl, Cfg: cfg, Reg: reg, Prompts: map[string]string{}, Store: store, HeadSHA: "headsha",
	})
	if err != nil {
		t.Fatalf("verify.Run: %v", err)
	}

	got := map[string]schema.Disposition{}
	for _, f := range out {
		got[f.Payload.Title] = f.Envelope.Verification.Disposition
	}
	if got["gate.fail_on weakened from warning to error"] != schema.DispositionAccepted {
		t.Errorf("fact disposition = %q, want accepted: code-computed blockers are exempt", got["gate.fail_on weakened from warning to error"])
	}
	if got["suspicious intent"] != schema.DispositionDropped {
		t.Errorf("model finding disposition = %q, want dropped by the groundedness verifier", got["suspicious intent"])
	}
}
