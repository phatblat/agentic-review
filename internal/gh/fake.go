package gh

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
)

// PostedCall records one write-side Port call so render/post tests can
// assert on structured calls instead of live API traffic. Only the fields
// relevant to Method are populated.
type PostedCall struct {
	Method   string          `json:"method"` // CreateIssueComment | EditIssueComment | ReactIssueComment | CreateReview | CreateFileComment
	Number   int             `json:"number,omitempty"`
	ID       int64           `json:"id,omitempty"`
	Body     string          `json:"body,omitempty"`
	HeadSHA  string          `json:"head_sha,omitempty"`
	Path     string          `json:"path,omitempty"`
	Content  string          `json:"content,omitempty"` // reaction content
	Comments []ReviewComment `json:"comments,omitempty"`
}

// Fake is a fixture-backed Port for tests. Load it from a fixture directory
// with LoadFake, or build one in-memory with NewFake for unit tests that
// don't need on-disk fixtures.
//
// Fixture directory layout, all optional except pr.json:
//
//	pr.json           gh.PullRequest, JSON
//	files.json         []gh.File, JSON
//	comments.json       []gh.Comment, JSON (pre-existing issue comments)
//	permissions.json    map[string login]permission, JSON
//	base/<path...>      file content at PullRequest.BaseSHA, mirrored by path
//	head/<path...>      file content at PullRequest.HeadSHA, mirrored by path
type Fake struct {
	mu sync.Mutex

	dir     string
	pr      *PullRequest
	files   []File
	commits []string
	perms   map[string]string

	comments      []Comment
	nextCommentID int64

	reviewComments      []Comment
	nextReviewCommentID int64

	Posted []PostedCall
}

var _ Port = (*Fake)(nil)

// NewFake builds an empty in-memory Fake seeded with pr; callers add files,
// comments, and permissions directly before use.
func NewFake(pr *PullRequest) *Fake {
	return &Fake{pr: pr, perms: map[string]string{}, nextCommentID: 1, nextReviewCommentID: 1}
}

// LoadFake loads a Fake from a fixture directory. Missing optional files
// yield empty collections rather than errors.
func LoadFake(dir string) (*Fake, error) {
	f := &Fake{dir: dir, perms: map[string]string{}, nextCommentID: 1, nextReviewCommentID: 1}

	if err := readJSONFile(filepath.Join(dir, "pr.json"), &f.pr); err != nil {
		return nil, fmt.Errorf("gh: load fake pr.json: %w", err)
	}
	if f.pr == nil {
		return nil, fmt.Errorf("gh: load fake %s: pr.json is required", dir)
	}
	if err := readOptionalJSONFile(filepath.Join(dir, "files.json"), &f.files); err != nil {
		return nil, fmt.Errorf("gh: load fake files.json: %w", err)
	}
	if err := readOptionalJSONFile(filepath.Join(dir, "commits.json"), &f.commits); err != nil {
		return nil, fmt.Errorf("gh: load fake commits.json: %w", err)
	}
	if err := readOptionalJSONFile(filepath.Join(dir, "comments.json"), &f.comments); err != nil {
		return nil, fmt.Errorf("gh: load fake comments.json: %w", err)
	}
	if err := readOptionalJSONFile(filepath.Join(dir, "review_comments.json"), &f.reviewComments); err != nil {
		return nil, fmt.Errorf("gh: load fake review_comments.json: %w", err)
	}
	if err := readOptionalJSONFile(filepath.Join(dir, "permissions.json"), &f.perms); err != nil {
		return nil, fmt.Errorf("gh: load fake permissions.json: %w", err)
	}
	for _, c := range f.comments {
		if c.ID >= f.nextCommentID {
			f.nextCommentID = c.ID + 1
		}
	}
	for _, c := range f.reviewComments {
		if c.ID >= f.nextReviewCommentID {
			f.nextReviewCommentID = c.ID + 1
		}
	}
	return f, nil
}

func readJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, v)
}

func readOptionalJSONFile(path string, v any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	return json.Unmarshal(data, v)
}

func (f *Fake) PullRequest(_ context.Context, _ Repo, _ int) (*PullRequest, error) {
	return f.pr, nil
}

func (f *Fake) ListFiles(_ context.Context, _ Repo, _ int) ([]File, error) {
	return f.files, nil
}

func (f *Fake) ListCommits(_ context.Context, _ Repo, _ int) ([]string, error) {
	return f.commits, nil
}

// FileContent reads dir/base/<path> when ref == pr.BaseSHA, dir/head/<path>
// when ref == pr.HeadSHA. Any other ref is a fixture bug and errors loudly.
func (f *Fake) FileContent(_ context.Context, _ Repo, path, ref string) ([]byte, error) {
	var side string
	switch ref {
	case f.pr.BaseSHA:
		side = "base"
	case f.pr.HeadSHA:
		side = "head"
	default:
		return nil, fmt.Errorf("gh: fake: unknown ref %q (want base %q or head %q)", ref, f.pr.BaseSHA, f.pr.HeadSHA)
	}
	if f.dir == "" {
		return nil, fmt.Errorf("gh: fake: no content directory configured for %s@%s", path, ref)
	}
	data, err := os.ReadFile(filepath.Join(f.dir, side, path))
	if err != nil {
		return nil, fmt.Errorf("gh: fake: read %s/%s: %w", side, path, err)
	}
	return data, nil
}

func (f *Fake) ListIssueComments(_ context.Context, _ Repo, _ int) ([]Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Comment, len(f.comments))
	copy(out, f.comments)
	return out, nil
}

func (f *Fake) CreateIssueComment(_ context.Context, _ Repo, number int, body string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := f.nextCommentID
	f.nextCommentID++
	f.comments = append(f.comments, Comment{ID: id, Body: body})
	f.Posted = append(f.Posted, PostedCall{Method: "CreateIssueComment", Number: number, ID: id, Body: body})
	return id, nil
}

func (f *Fake) EditIssueComment(_ context.Context, _ Repo, id int64, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := false
	for i := range f.comments {
		if f.comments[i].ID == id {
			f.comments[i].Body = body
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("gh: fake: edit unknown comment %d", id)
	}
	f.Posted = append(f.Posted, PostedCall{Method: "EditIssueComment", ID: id, Body: body})
	return nil
}

func (f *Fake) ReactIssueComment(_ context.Context, _ Repo, id int64, content string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Posted = append(f.Posted, PostedCall{Method: "ReactIssueComment", ID: id, Content: content})
	return nil
}

func (f *Fake) CreateReview(_ context.Context, _ Repo, number int, headSHA, body string, comments []ReviewComment) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Posted = append(f.Posted, PostedCall{Method: "CreateReview", Number: number, HeadSHA: headSHA, Body: body, Comments: comments})
	for _, c := range comments {
		f.reviewComments = append(f.reviewComments, Comment{ID: f.nextReviewCommentID, Body: c.Body})
		f.nextReviewCommentID++
	}
	return nil
}

func (f *Fake) CreateFileComment(_ context.Context, _ Repo, number int, headSHA, path, body string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Posted = append(f.Posted, PostedCall{Method: "CreateFileComment", Number: number, HeadSHA: headSHA, Path: path, Body: body})
	f.reviewComments = append(f.reviewComments, Comment{ID: f.nextReviewCommentID, Body: body})
	f.nextReviewCommentID++
	return nil
}

func (f *Fake) ListReviewComments(_ context.Context, _ Repo, _ int) ([]Comment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Comment, len(f.reviewComments))
	copy(out, f.reviewComments)
	return out, nil
}

func (f *Fake) PermissionLevel(_ context.Context, _ Repo, user string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if level, ok := f.perms[user]; ok {
		return level, nil
	}
	return "none", nil
}
