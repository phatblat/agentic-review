package verify

import (
	"context"
	"fmt"
	"sort"

	"github.com/phatblat/agentic-review/internal/schema"
)

// Duplication is spec §7's duplication lens: pure code, no model. Findings
// merge when their fingerprints are equal, or when (path, anchor start,
// anchor end) are equal and their normalised claim stems match. The
// survivor is the highest severity, tie-broken by highest confidence then
// lowest envelope.id; every contributing persona is credited on the
// survivor's envelope; losers get disposition merged.
type Duplication struct{}

func (Duplication) Name() string { return "duplication" }

func (Duplication) Apply(_ context.Context, in []schema.Finding, _ Env) ([]schema.Finding, []Verdict, error) {
	out := make([]schema.Finding, len(in))
	copy(out, in)

	idx := acceptedOnly(in)
	if len(idx) < 2 {
		return out, nil, nil
	}

	uf := newUnionFind(len(idx))
	byFingerprint := map[string]int{} // fingerprint -> first local index seen
	byAnchorClaim := map[string]int{} // path|start|end|claimstem -> first local index seen
	for i, j := range idx {
		f := in[j]
		if first, ok := byFingerprint[f.Envelope.Fingerprint]; ok {
			uf.union(first, i)
		} else {
			byFingerprint[f.Envelope.Fingerprint] = i
		}
		key := fmt.Sprintf("%s|%d|%d|%s", f.Payload.Anchor.Path, f.Payload.Anchor.StartLine, f.Payload.Anchor.EndLine, claimStem(f.Payload.Claim))
		if first, ok := byAnchorClaim[key]; ok {
			uf.union(first, i)
		} else {
			byAnchorClaim[key] = i
		}
	}

	groups := map[int][]int{} // union-find root (local index) -> member local indices
	for i := range idx {
		root := uf.find(i)
		groups[root] = append(groups[root], i)
	}

	var verdicts []Verdict
	for _, members := range groups {
		if len(members) < 2 {
			continue
		}
		globalMembers := make([]int, len(members))
		for i, m := range members {
			globalMembers[i] = idx[m]
		}
		survivor := pickSurvivor(in, globalMembers)

		personas := make([]string, 0, len(globalMembers))
		for _, g := range globalMembers {
			personas = append(personas, in[g].Envelope.Persona)
		}
		sort.Strings(personas)
		out[survivor].Envelope.MergedPersonas = personas

		v := Verdict{Lens: "duplication", Result: "fail", Checked: "mechanical", Reason: "merged with a duplicate finding"}
		for _, g := range globalMembers {
			if g == survivor {
				continue
			}
			out[g].Envelope.Verification.Verdicts = append(out[g].Envelope.Verification.Verdicts, schema.EnvelopeVerdict{
				Lens: v.Lens, Result: v.Result, Checked: v.Checked, Reason: v.Reason,
			})
			out[g].Envelope.Verification.Disposition = schema.DispositionMerged
			verdicts = append(verdicts, v)
		}
	}
	return out, verdicts, nil
}

// pickSurvivor returns the global index (into in) of the highest-severity
// member, tie-broken by highest confidence then lowest envelope.id.
func pickSurvivor(in []schema.Finding, globalMembers []int) int {
	best := globalMembers[0]
	for _, g := range globalMembers[1:] {
		a, b := in[g], in[best]
		switch {
		case severityRank(a.Payload.Severity) != severityRank(b.Payload.Severity):
			if severityRank(a.Payload.Severity) > severityRank(b.Payload.Severity) {
				best = g
			}
		case a.Payload.Confidence != b.Payload.Confidence:
			if a.Payload.Confidence > b.Payload.Confidence {
				best = g
			}
		case a.Envelope.ID < b.Envelope.ID:
			best = g
		}
	}
	return best
}

func severityRank(s string) int {
	for i, sev := range schema.Severities {
		if sev == s {
			return i
		}
	}
	return -1
}

// unionFind is a minimal disjoint-set over [0, n) local indices, used to
// group findings transitively linked by either merge condition.
type unionFind struct{ parent []int }

func newUnionFind(n int) *unionFind {
	p := make([]int, n)
	for i := range p {
		p[i] = i
	}
	return &unionFind{parent: p}
}

func (u *unionFind) find(x int) int {
	for u.parent[x] != x {
		u.parent[x] = u.parent[u.parent[x]]
		x = u.parent[x]
	}
	return x
}

func (u *unionFind) union(a, b int) {
	ra, rb := u.find(a), u.find(b)
	if ra != rb {
		u.parent[ra] = rb
	}
}
