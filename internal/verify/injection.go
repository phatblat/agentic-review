package verify

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/phatblat/agentic-review/internal/infer"
	"github.com/phatblat/agentic-review/internal/logx"
	"github.com/phatblat/agentic-review/internal/schema"
)

const injectionPersonaID = "verifier/injection"

// urlRE finds bare URLs across title/claim/suggested_fix text.
var urlRE = regexp.MustCompile(`https?://[^\s)>\]"']+`)

// longEncodedRunRE matches a base64/hex-alphabet run of 64+ characters —
// long enough to rule out incidental identifiers or hashes appearing in
// ordinary review commentary.
var longEncodedRunRE = regexp.MustCompile(`[A-Za-z0-9+/=]{64,}`)

// injectionPatterns is spec item 35's case-insensitive phrase list.
var injectionPatterns = regexp.MustCompile(`(?i)ignore (all )?previous instructions|disregard the above|you are now|system:|<\|im_start\|>|\bcurl\b|\bwget\b|base64 -d`)

// allowedURLHosts is every host an injection-screened finding's own text
// may reference without tripping the mechanical screen.
func allowedURLHost(host string) bool {
	host = strings.ToLower(host)
	return host == "github.com" || strings.HasSuffix(host, ".githubusercontent.com")
}

// Injection is spec §7's injection lens: mechanical first, model second.
// The mechanical screen covers title, claim, and suggested_fix.replacement
// for suspicious URLs, long encoded runs, and known manipulation phrases;
// a hit fails immediately with no model call. Content that clears the
// mechanical screen still goes to the verify capability, which judges
// manipulation intent in subtler content. Fail (either stage) drops the
// finding — its content is never re-rendered (spec §8.3).
type Injection struct{}

func (Injection) Name() string { return "injection" }

func (Injection) Apply(ctx context.Context, in []schema.Finding, env Env) ([]schema.Finding, []Verdict, error) {
	out := make([]schema.Finding, len(in))
	copy(out, in)

	idx := acceptedOnly(in)
	if len(idx) == 0 {
		return out, nil, nil
	}

	var verdicts []Verdict
	var modelIdx []int
	for _, j := range idx {
		f := in[j]
		if reason, hit := mechanicalInjectionScreen(f.Payload); hit {
			v := Verdict{Lens: "injection", Result: "fail", Checked: "mechanical", Reason: reason}
			out[j].Envelope.Verification.Verdicts = append(out[j].Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
				Lens: v.Lens, Result: v.Result, Checked: v.Checked, Reason: v.Reason,
			})
			out[j].Envelope.Verification.Disposition = schema.DispositionDropped
			logx.Warn("verify: injection: %s: %s", f.Envelope.ID, reason)
			verdicts = append(verdicts, v)
			continue
		}
		modelIdx = append(modelIdx, j)
	}
	if len(modelIdx) == 0 {
		return out, verdicts, nil
	}

	subjects := make([]schema.Finding, len(modelIdx))
	for i, j := range modelIdx {
		subjects[i] = in[j]
	}
	results, err := callVerifyBatch(ctx, env, injectionPersonaID, subjects, renderInjectionBatch(subjects))
	if err != nil {
		return nil, nil, err
	}
	for _, j := range modelIdx {
		f := in[j]
		mv, ok := results[f.Envelope.ID]
		if !ok {
			logMissingVerdict("injection", f.Envelope.ID)
			continue
		}
		v := Verdict{Lens: "injection", Result: mv.Result, Checked: "mechanical+model", Reason: mv.Reason}
		out[j].Envelope.Verification.Verdicts = append(out[j].Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
			Lens: v.Lens, Result: v.Result, Checked: v.Checked, Reason: v.Reason,
		})
		if mv.Result == "fail" {
			out[j].Envelope.Verification.Disposition = schema.DispositionDropped
			logx.Warn("verify: injection: %s: model judged manipulative intent", f.Envelope.ID)
		}
		verdicts = append(verdicts, v)
	}
	return out, verdicts, nil
}

// mechanicalInjectionScreen checks title, claim, and suggested_fix.replacement
// for a disallowed URL host, a long base64/hex run, or a known manipulation
// phrase, returning the first hit found.
func mechanicalInjectionScreen(p schema.Payload) (reason string, hit bool) {
	fix := ""
	if p.SuggestedFix != nil {
		fix = p.SuggestedFix.Replacement
	}
	fields := map[string]string{"title": p.Title, "claim": p.Claim, "suggested_fix.replacement": fix}
	for _, field := range []string{"title", "claim", "suggested_fix.replacement"} {
		text := fields[field]
		if text == "" {
			continue
		}
		for _, raw := range urlRE.FindAllString(text, -1) {
			u, err := url.Parse(raw)
			if err != nil || !allowedURLHost(u.Hostname()) {
				return fmt.Sprintf("%s contains a URL to a disallowed host: %s", field, raw), true
			}
		}
		if longEncodedRunRE.MatchString(text) {
			return fmt.Sprintf("%s contains a long base64/hex-alphabet run", field), true
		}
		if m := injectionPatterns.FindString(text); m != "" {
			return fmt.Sprintf("%s matches a known manipulation pattern: %q", field, m), true
		}
	}
	return "", false
}

func renderInjectionBatch(subjects []schema.Finding) string {
	var b strings.Builder
	b.WriteString("Findings to judge — each already cleared the mechanical injection screen:\n\n")
	for _, f := range subjects {
		fmt.Fprintf(&b, "finding_id: %s\n", f.Envelope.ID)
		b.WriteString(infer.WrapUntrusted("title:"+f.Envelope.ID, f.Payload.Title))
		b.WriteString("\n")
		b.WriteString(infer.WrapUntrusted("claim:"+f.Envelope.ID, f.Payload.Claim))
		b.WriteString("\n")
		if f.Payload.SuggestedFix != nil {
			b.WriteString(infer.WrapUntrusted("suggested_fix:"+f.Envelope.ID, f.Payload.SuggestedFix.Replacement))
			b.WriteString("\n")
		}
		b.WriteString("---\n")
	}
	return b.String()
}
