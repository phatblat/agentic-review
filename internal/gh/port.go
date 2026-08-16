// Package gh defines the GitHub access surface every pipeline stage depends
// on (Port), a go-github-backed implementation, a content-fetching cache
// (ContentStore), and a fixture-backed fake for tests.
package gh

import "context"

// Repo identifies a GitHub repository.
type Repo struct {
	Owner string
	Name  string
}

// PullRequest is the subset of GitHub pull request state agentic-review
// needs, fetched fresh via Port.PullRequest — never trusted from a webhook
// payload, which can be stale by the time a run acts on it.
type PullRequest struct {
	Number  int      `json:"number"`
	Title   string   `json:"title"`
	Body    string   `json:"body"`
	Draft   bool     `json:"draft"`
	Commits int      `json:"commits"`
	Labels  []string `json:"labels"`

	BaseRef string `json:"base_ref"`
	BaseSHA string `json:"base_sha"`
	HeadRef string `json:"head_ref"`
	HeadSHA string `json:"head_sha"`

	// IsFork is true when the head repository differs from the base
	// repository, or the head repository is unavailable (e.g. the fork was
	// deleted after opening the PR) — the safer assumption in either case.
	IsFork bool `json:"is_fork"`

	// AuthorAssociation is the raw GitHub value: "OWNER", "MEMBER",
	// "COLLABORATOR", "CONTRIBUTOR", "FIRST_TIME_CONTRIBUTOR", "FIRST_TIMER",
	// "MANNEQUIN", or "NONE". Callers map it to facts.Assoc.
	AuthorAssociation string `json:"author_association"`
}

// File is one changed file in a pull request diff.
type File struct {
	Path         string `json:"path"`          // CommitFile.Filename
	PreviousPath string `json:"previous_path"` // CommitFile.PreviousFilename, "" if not a rename
	Status       string `json:"status"`        // added|removed|modified|renamed|copied|changed|unchanged
	Additions    int    `json:"additions"`
	Deletions    int    `json:"deletions"`
	Patch        string `json:"patch"` // "" for binary files and for files over GitHub's patch limit
}

// Comment is an issue (i.e. PR conversation) comment.
type Comment struct {
	ID     int64  `json:"id"`
	Body   string `json:"body"`
	UserID int64  `json:"user_id"`
	IsBot  bool   `json:"is_bot"`
}

// ReviewComment is one comment to attach to a batched pull request review.
type ReviewComment struct {
	Path      string
	Line      int    // new-side line for Side=="RIGHT", old-side for "LEFT"
	StartLine int    // 0 when single-line
	Side      string // "RIGHT" | "LEFT"
	Body      string
}

// Port is the GitHub access surface every pipeline stage depends on. The
// go-github adapter (github.go) and the fixture-backed Fake (fake.go) both
// implement it.
type Port interface {
	PullRequest(ctx context.Context, r Repo, number int) (*PullRequest, error)
	ListFiles(ctx context.Context, r Repo, number int) ([]File, error)
	// ListCommits lists every commit message on the pull request (oldest
	// first, GitHub's own order), for personas whose inputs.context
	// includes commit-messages.
	ListCommits(ctx context.Context, r Repo, number int) ([]string, error)
	FileContent(ctx context.Context, r Repo, path, ref string) ([]byte, error)
	ListIssueComments(ctx context.Context, r Repo, number int) ([]Comment, error)
	CreateIssueComment(ctx context.Context, r Repo, number int, body string) (int64, error)
	EditIssueComment(ctx context.Context, r Repo, id int64, body string) error
	ReactIssueComment(ctx context.Context, r Repo, id int64, content string) error
	CreateReview(ctx context.Context, r Repo, number int, headSHA, body string, comments []ReviewComment) error
	CreateFileComment(ctx context.Context, r Repo, number int, headSHA, path, body string) error
	// ListReviewComments lists every review comment (posted via
	// CreateReview or CreateFileComment) on the pull request — distinct
	// from ListIssueComments' PR-conversation comments — so internal/post
	// can dedup finding comments by parsed marker fingerprint across runs.
	ListReviewComments(ctx context.Context, r Repo, number int) ([]Comment, error)
	PermissionLevel(ctx context.Context, r Repo, user string) (string, error)
}
