package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/phatblat/agentic-review/internal/artifact"
	"github.com/phatblat/agentic-review/internal/config"
	"github.com/phatblat/agentic-review/internal/facts"
	"github.com/phatblat/agentic-review/internal/gate"
	"github.com/phatblat/agentic-review/internal/gh"
	"github.com/phatblat/agentic-review/internal/ghevent"
	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/localconfig"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/osv"
	"github.com/phatblat/agentic-review/internal/post"
	"github.com/phatblat/agentic-review/internal/render"
	"github.com/phatblat/agentic-review/internal/roster"
	"github.com/phatblat/agentic-review/internal/schema"
	"github.com/phatblat/agentic-review/internal/validate"
	"github.com/phatblat/agentic-review/internal/verify"
)

// ReviewDeps bundles everything Review needs beyond the raw webhook
// payload.
type ReviewDeps struct {
	Port          gh.Port
	Client        infer.Client
	OSVClient     *osv.Client
	DepsDevClient *osv.DepsDevClient

	// ConfigRoot is the checked-out repository root localconfig.Load
	// reads config.yaml and repo-local personas from.
	ConfigRoot string

	RunID  int64
	RunURL string

	// OutDir overrides artifact.New's resolved output directory; "" uses
	// its env-derived defaults.
	OutDir string
	// RecordDir, when non-empty, records every model request/response
	// pair per persona under this directory (spec §13.3); "" disables
	// recording. Matches the `triage`/`plan` subcommands' own --record
	// flag convention.
	RecordDir string
	// ReplayDir, when non-empty, replays recordings from this directory
	// instead of ever calling Client — mutually exclusive with
	// RecordDir; a non-empty ReplayDir always wins. Used by
	// fixtures/replay/-driven tests to drive the whole pipeline
	// deterministically with no live model calls.
	ReplayDir string
	// DryRun, when true, skips triage (no model call at all), computes
	// the roster from a nil assessment (the same fallback path as a
	// failed triage), writes the roster table to $GITHUB_STEP_SUMMARY,
	// and returns 0 — no tier-2 execution, no verification, no
	// posting. Used by the shim smoke test (spec item 9) to prove the
	// binary is reachable and dispatches correctly with zero network
	// access.
	DryRun bool
	// EndpointOverride, when non-empty, fills in any models[] binding
	// whose Endpoint config.yaml left blank (action.yml's `endpoint`
	// input: a shared default for repos that omit per-capability
	// endpoints). It never replaces an endpoint config.yaml did set.
	EndpointOverride string
	// FailOnOverride, when non-empty, replaces cfg.Review.Gate.FailOn
	// after config load (action.yml's `fail_on` input, "override,
	// default empty" per spec item 46).
	FailOnOverride string
}

// Review is the single entrypoint every review run hangs off (spec §9,
// item 41a), in exactly this order:
//  1. ghevent.Parse -> ghevent.Gate. A skip exits 0 without posting.
//  2. The issue_comment summons path (permission check, ack-on-denial,
//     eyes reaction, scope restriction).
//  3. Load + validate + lint config from the head ref.
//  4. facts.Assemble -> gate.Skip.
//  5. deps.DryRun short-circuits here: roster from a nil (fallback)
//     assessment, step summary written, exit 0 — no model calls.
//  6. Triage -> roster -> tier 2 -> lenses -> post -> artifact -> gate.Exit.
//
// Any step returning an infra/config error short-circuits to the error
// summary variant plus exit 1.
func Review(ctx context.Context, eventName string, payload []byte, deps ReviewDeps) int {
	ev, err := ghevent.Parse(eventName, payload)
	if err != nil {
		logx.Error("review: parse event: %v", err)
		return 1
	}

	priorSummary, priorHistory := false, ([]render.HistoryEntry)(nil)
	if ev.Kind == ghevent.KindPullRequest {
		if comments, err := deps.Port.ListIssueComments(ctx, ev.Repo, ev.PRNumber); err == nil {
			for _, c := range comments {
				if m, ok := render.Parse(c.Body); ok && m.Kind == "summary" {
					priorSummary = true
					priorHistory = decodeHistory(m.Fields["history"])
				}
			}
		} else {
			logx.Debug("review: list issue comments for prior-summary check: %v", err)
		}
	}

	proceed, reason := ghevent.Gate(ev, priorSummary)
	if !proceed {
		logx.Debug("review: skip: %s", reason)
		return 0
	}

	var scopeIDs []string
	if ev.Kind == ghevent.KindIssueComment {
		if exit, ok := handleSummons(ctx, ev, deps, &scopeIDs); ok {
			return exit
		}
	}

	reg, prompts, cfg, err := localconfig.Load(deps.ConfigRoot)
	if err != nil {
		return failInfra(ctx, deps, ev, "config load", err, priorHistory)
	}
	applyOverrides(cfg, deps)
	if checks, err := validate.All(reg, cfg); err != nil {
		return failInfra(ctx, deps, ev, "config validate", err, priorHistory)
	} else if failed := firstFailedCheck(checks); failed != nil {
		return failInfra(ctx, deps, ev, "config lint", fmt.Errorf("%s: %w", failed.Slot, failed.Err), priorHistory)
	}

	store := gh.NewContentStore(deps.Port, ev.Repo)
	f, fileClasses, pr, files, err := facts.Assemble(ctx, deps.Port, store, ev, cfg)
	if err != nil {
		return failInfra(ctx, deps, ev, "facts assemble", err, priorHistory)
	}

	skip, skipReason, err := gate.Skip(f, cfg)
	if err != nil {
		return failInfra(ctx, deps, ev, "gate skip", err, priorHistory)
	}
	if skip {
		return postSkip(ctx, deps, ev, skipReason, priorHistory)
	}

	if deps.DryRun {
		rst, err := roster.Compute(reg, f, nil, cfg)
		if err != nil {
			return failInfra(ctx, deps, ev, "roster compute", err, priorHistory)
		}
		rst = restrictToScope(rst, scopeIDs)
		_ = logx.StepSummary(rst.StepSummaryTable("## agentic-review"))
		return 0
	}

	art, err := artifact.New(deps.OutDir)
	if err != nil {
		return failInfra(ctx, deps, ev, "artifact init", err, priorHistory)
	}

	start := time.Now()

	assessment, triageErr := RunTriage(ctx, infer.Select(deps.Client, deps.ReplayDir, deps.RecordDir, "triage"), reg, prompts, cfg, f, TriageInput{
		Title: pr.Title, Body: pr.Body, Commits: commitMessages(ctx, deps, ev),
	})
	fallbackRosterUsed := triageErr != nil
	if triageErr != nil {
		logx.Warn("review: triage: %v (falling back to logic, security, verifier/groundedness)", triageErr)
	}
	_ = art.WriteTriage(assessment)

	rst, err := roster.Compute(reg, f, assessment, cfg)
	if err != nil {
		return failInfra(ctx, deps, ev, "roster compute", err, priorHistory)
	}
	rst = restrictToScope(rst, scopeIDs)
	_ = art.WriteRoster(rst)
	_ = logx.StepSummary(rst.StepSummaryTable("## agentic-review"))

	diffPaths := make(map[string]bool, len(files))
	for _, file := range files {
		diffPaths[file.Path] = true
	}

	teamDeps := TeamDeps{
		Client: deps.Client, Cfg: cfg, Facts: f, Assessment: assessment, Prompts: prompts,
		Files: files, FileClasses: fileClasses, Store: store, BaseSHA: pr.BaseSHA, HeadSHA: pr.HeadSHA,
		PR:            PRContext{Title: pr.Title, Body: pr.Body, Commits: commitMessages(ctx, deps, ev)},
		OSVClient:     deps.OSVClient,
		DepsDevClient: deps.DepsDevClient,
		RecordDir:     deps.RecordDir,
		ReplayDir:     deps.ReplayDir,
	}
	findings, budget, err := RunTeam(ctx, teamDeps, reg, rst)
	if err != nil {
		return failInfra(ctx, deps, ev, "tier-2 execution", err, priorHistory)
	}

	verifyEnv := verify.Env{
		Client: deps.Client, Cfg: cfg, Reg: reg, Prompts: prompts, Store: store,
		HeadSHA: pr.HeadSHA, IsFork: pr.IsFork, Facts: f, RecordDir: deps.RecordDir, ReplayDir: deps.ReplayDir,
	}
	findings, verdicts, err := verify.Run(ctx, findings, verifyEnv)
	if err != nil {
		return failInfra(ctx, deps, ev, "verification lenses", err, priorHistory)
	}

	_ = art.WriteFindingsRaw(findings)
	_ = art.WriteVerdicts(verdicts)
	_ = art.WriteFindingsFinal(findings)
	_ = art.WriteBudget(budget)

	history := append([]render.HistoryEntry{{
		Run: deps.RunID, URL: deps.RunURL, Trigger: ev.Trigger(),
		Team: memberIDs(rst), Tokens: totalConsumed(budget), MS: time.Since(start).Milliseconds(),
	}}, priorHistory...)

	if err := post.Post(ctx, post.Input{
		Port: deps.Port, Repo: ev.Repo, Number: ev.PRNumber, HeadSHA: pr.HeadSHA, IsFork: pr.IsFork,
		RunID: deps.RunID, RunURL: deps.RunURL, Trigger: ev.Trigger(),
		Findings: findings, DiffPaths: diffPaths, Caps: cfg.Review.Caps,
		Team: teamFooter(rst), TotalTokens: totalConsumed(budget), Duration: time.Since(start), History: history,
		FallbackRosterUsed: fallbackRosterUsed,
	}); err != nil {
		logx.Error("review: post: %v", err)
		return 1
	}

	writeActionOutputs(post.SeverityCounts(findings), totalConsumed(budget), rst)
	return gate.Exit(findings, cfg, f, false)
}

// handleSummons implements spec §11.2's issue_comment path: skip-fast on
// the bot's own marker echo, a permission check with an ack-comment on
// denial, an eyes reaction on success, and scope-argument parsing. ok is
// true when Review should return immediately with the given exit code.
func handleSummons(ctx context.Context, ev *ghevent.Event, deps ReviewDeps, scopeIDs *[]string) (exit int, ok bool) {
	if m, parsed := render.Parse(ev.CommentBody); parsed && m.Kind != "" {
		return 0, true // the bot's own marker echoed back in a quote/reply
	}

	level, err := deps.Port.PermissionLevel(ctx, ev.Repo, ev.CommentAuthor)
	if err != nil {
		logx.Error("review: permission level for %s: %v", ev.CommentAuthor, err)
		return 1, true
	}
	if level != "admin" && level != "write" {
		ack := render.Render(render.Marker{Kind: "ack", Fields: map[string]string{
			"run": strconv.FormatInt(deps.RunID, 10), "in-reply-to": strconv.FormatInt(ev.CommentID, 10),
		}})
		body := fmt.Sprintf("%s\nagentic-review requires write access to run; @%s has %s.", ack, ev.CommentAuthor, level)
		if _, err := deps.Port.CreateIssueComment(ctx, ev.Repo, ev.PRNumber, body); err != nil {
			logx.Error("review: post permission-denial ack: %v", err)
		}
		return 0, true
	}

	if err := deps.Port.ReactIssueComment(ctx, ev.Repo, ev.CommentID, "eyes"); err != nil {
		logx.Warn("review: react to summons comment: %v", err)
	}
	*scopeIDs = parseScopeArgs(ev.CommentBody)
	return 0, false
}

// parseScopeArgs extracts persona ids from "/agentic-review <id> <id>...".
func parseScopeArgs(body string) []string {
	idx := strings.Index(body, "/agentic-review")
	if idx < 0 {
		return nil
	}
	return strings.Fields(body[idx+len("/agentic-review"):])
}

// restrictToScope intersects rst's members with scopeIDs (spec item 2:
// "restrict the tier-2 team to the named persona ids intersected with the
// computed roster; unknown ids are reported with logx.Warn and ignored").
// An empty scopeIDs (no summons scope argument) returns rst unchanged.
func restrictToScope(rst *roster.Roster, scopeIDs []string) *roster.Roster {
	if len(scopeIDs) == 0 {
		return rst
	}
	want := make(map[string]bool, len(scopeIDs))
	for _, id := range scopeIDs {
		want[id] = true
	}
	have := make(map[string]bool, len(rst.Members))
	var kept []roster.Member
	for _, m := range rst.Members {
		have[m.ID] = true
		if want[m.ID] {
			kept = append(kept, m)
		}
	}
	for id := range want {
		if !have[id] {
			logx.Warn("review: scope argument %q does not match any roster member; ignored", id)
		}
	}
	rst.Members = kept
	return rst
}

// commitMessages fetches commit messages for personas whose
// inputs.context includes commit-messages; a fetch failure degrades to
// no commit context rather than failing the run.
func commitMessages(ctx context.Context, deps ReviewDeps, ev *ghevent.Event) []string {
	commits, err := deps.Port.ListCommits(ctx, ev.Repo, ev.PRNumber)
	if err != nil {
		logx.Debug("review: list commits: %v", err)
		return nil
	}
	return commits
}

// applyOverrides fills in cfg fields action.yml exposes as inputs but
// config.yaml may leave unset (spec item 46). Endpoint only fills a
// blank binding — config.yaml's own value always wins; FailOn replaces
// cfg.Review.Gate.FailOn outright when set, matching its documented
// "override" semantics.
func applyOverrides(cfg *config.Config, deps ReviewDeps) {
	if deps.EndpointOverride != "" {
		for cap, mb := range cfg.Models {
			if mb.Endpoint == "" {
				mb.Endpoint = deps.EndpointOverride
				cfg.Models[cap] = mb
			}
		}
	}
	if deps.FailOnOverride != "" {
		cfg.Review.Gate.FailOn = deps.FailOnOverride
	}
}

// firstFailedCheck returns the first RuleCheck with a non-nil Err, or nil
// if every rule compiled and lint-checked cleanly.
func firstFailedCheck(checks []validate.RuleCheck) *validate.RuleCheck {
	for i := range checks {
		if checks[i].Err != nil {
			return &checks[i]
		}
	}
	return nil
}

// failInfra logs an ::error:: and posts the error summary variant plus
// exit 1 (spec item 41a).
func failInfra(ctx context.Context, deps ReviewDeps, ev *ghevent.Event, stage string, err error, priorHistory []render.HistoryEntry) int {
	logx.Error("review: %s: %v", stage, err)
	postErr := post.Post(ctx, post.Input{
		Port: deps.Port, Repo: ev.Repo, Number: ev.PRNumber,
		RunID: deps.RunID, RunURL: deps.RunURL, Trigger: ev.Trigger(),
		ErrorStage: stage, History: priorHistory,
	})
	if postErr != nil {
		logx.Error("review: post error summary: %v", postErr)
	}
	return 1
}

// postSkip renders and upserts the tier-0 skip summary and writes the
// (facts-only) artifact, per spec item 41a step 4.
func postSkip(ctx context.Context, deps ReviewDeps, ev *ghevent.Event, reason string, priorHistory []render.HistoryEntry) int {
	if art, err := artifact.New(deps.OutDir); err != nil {
		logx.Warn("review: artifact init on skip: %v", err)
	} else if err := art.WriteFindingsFinal(nil); err != nil {
		logx.Warn("review: write skip artifact: %v", err)
	}

	history := append([]render.HistoryEntry{{
		Run: deps.RunID, URL: deps.RunURL, Trigger: ev.Trigger(), Team: nil, Tokens: 0, MS: 0,
	}}, priorHistory...)

	if err := post.Post(ctx, post.Input{
		Port: deps.Port, Repo: ev.Repo, Number: ev.PRNumber,
		RunID: deps.RunID, RunURL: deps.RunURL, Trigger: ev.Trigger(),
		SkipReason: reason, History: history,
	}); err != nil {
		logx.Error("review: post skip summary: %v", err)
		return 1
	}
	writeActionOutputs(nil, 0, nil)
	return 0
}

// writeActionOutputs writes action.yml's six declared outputs (spec item
// 46) to $GITHUB_OUTPUT: blocker/error/warning/nit counts, total
// consumed tokens, and a compact-JSON roster (rst may be nil on the
// tier-0 skip path, where every count is zero and roster is omitted).
func writeActionOutputs(counts map[string]int, tokens int, rst *roster.Roster) {
	for _, sev := range schema.Severities {
		_ = logx.Output(sev, strconv.Itoa(counts[sev]))
	}
	_ = logx.Output("tokens", strconv.Itoa(tokens))
	if rst == nil {
		_ = logx.Output("roster", "null")
		return
	}
	if b, err := json.Marshal(rst); err == nil {
		_ = logx.Output("roster", string(b))
	} else {
		logx.Debug("review: marshal roster for output: %v", err)
	}
}

func memberIDs(rst *roster.Roster) []string {
	ids := make([]string, len(rst.Members))
	for i, m := range rst.Members {
		ids[i] = m.ID
	}
	return ids
}

func teamFooter(rst *roster.Roster) []render.TeamMember {
	out := make([]render.TeamMember, len(rst.Members))
	for i, m := range rst.Members {
		out[i] = render.TeamMember{ID: m.ID, ResolvedModel: m.ResolvedModel}
	}
	return out
}

func totalConsumed(b *roster.Budget) int {
	total := 0
	for _, n := range b.Consumed {
		total += n
	}
	return total
}

// decodeHistory decodes a summary marker's history field (compact JSON
// array); a malformed or absent value decodes to nil rather than erroring
// — history is best-effort footer content, never load-bearing.
func decodeHistory(encoded string) []render.HistoryEntry {
	if encoded == "" {
		return nil
	}
	var out []render.HistoryEntry
	if err := json.Unmarshal([]byte(encoded), &out); err != nil {
		logx.Debug("review: decode prior history: %v", err)
		return nil
	}
	return out
}
