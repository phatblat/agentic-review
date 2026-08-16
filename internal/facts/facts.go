// Package facts is the CEL-facing and JSON-facing fact model: the frozen
// namespace of spec §5.1. Field tags are load-bearing — internal/activation
// registers these exact types with CEL using the `cel:` struct tags, and
// internal/artifact round-trips the `json:` tags into triage.json.
package facts

// Facts is the complete tier-0 fact model assembled for one run.
type Facts struct {
	PR   PR   `cel:"pr"   json:"pr"`
	Diff Diff `cel:"diff" json:"diff"`
	Deps Deps `cel:"deps" json:"deps"`
}

// PR is the pull-request-level facts.
type PR struct {
	Number            int      `cel:"number"             json:"number"`
	BaseRef           string   `cel:"base_ref"           json:"base_ref"`
	HeadSHA           string   `cel:"head_sha"           json:"head_sha"`
	IsFork            bool     `cel:"is_fork"            json:"is_fork"`
	AuthorAssociation Assoc    `cel:"author_association" json:"author_association"`
	Labels            []string `cel:"labels"             json:"labels"`
	Draft             bool     `cel:"draft"              json:"draft"`
	Commits           int      `cel:"commits"            json:"commits"`
}

// Diff is the diff-level facts.
type Diff struct {
	FilesChanged        int            `cel:"files_changed"         json:"files_changed"`
	Additions           int            `cel:"additions"             json:"additions"`
	Deletions           int            `cel:"deletions"             json:"deletions"`
	Languages           map[string]int `cel:"languages"              json:"languages"`
	Paths               []string       `cel:"paths"                  json:"paths"`
	Classes             []string       `cel:"classes"                json:"classes"`
	TouchesReviewConfig bool           `cel:"touches_review_config"  json:"touches_review_config"`
	TouchesWorkflows    bool           `cel:"touches_workflows"      json:"touches_workflows"`
	BinaryFiles         int            `cel:"binary_files"           json:"binary_files"`
	MaxFileAdditions    int            `cel:"max_file_additions"     json:"max_file_additions"`
}

// DepChange is one dependency version change observed in the diff.
type DepChange struct {
	Ecosystem string `cel:"ecosystem" json:"ecosystem"`
	Name      string `cel:"name"      json:"name"`
	From      string `cel:"from"      json:"from"`
	To        string `cel:"to"        json:"to"`
	Bump      string `cel:"bump"      json:"bump"` // major|minor|patch|prerelease|other
	// Path is the manifest or lockfile path the change was observed in —
	// additive to spec §5.1's frozen namespace (no existing rule can break
	// by gaining a field), used by builtin/dep-risk to anchor findings.
	Path string `cel:"path" json:"path"`
}

// Deps is the dependency-change facts.
type Deps struct {
	Changed []DepChange `cel:"changed" json:"changed"`
}
