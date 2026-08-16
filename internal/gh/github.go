package gh

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/google/go-github/v75/github"
)

// GitHub is the go-github-backed Port implementation.
type GitHub struct {
	client *github.Client
}

var _ Port = (*GitHub)(nil)

// NewGitHub builds a Port backed by the real GitHub API, authenticated with
// token and retrying rate-limit/5xx responses per the policy in retry.go.
func NewGitHub(token string) *GitHub {
	hc := &http.Client{Transport: &retryTransport{base: http.DefaultTransport}}
	return &GitHub{client: github.NewClient(hc).WithAuthToken(token)}
}

func (g *GitHub) PullRequest(ctx context.Context, r Repo, number int) (*PullRequest, error) {
	pr, _, err := g.client.PullRequests.Get(ctx, r.Owner, r.Name, number)
	if err != nil {
		return nil, fmt.Errorf("gh: get pull request %s/%s#%d: %w", r.Owner, r.Name, number, err)
	}

	labels := make([]string, 0, len(pr.Labels))
	for _, l := range pr.Labels {
		labels = append(labels, l.GetName())
	}

	baseFullName := pr.GetBase().GetRepo().GetFullName()
	headRepo := pr.GetHead().GetRepo()
	isFork := headRepo == nil || headRepo.GetFullName() != baseFullName

	return &PullRequest{
		Number:            pr.GetNumber(),
		Title:             pr.GetTitle(),
		Body:              pr.GetBody(),
		Draft:             pr.GetDraft(),
		Commits:           pr.GetCommits(),
		Labels:            labels,
		BaseRef:           pr.GetBase().GetRef(),
		BaseSHA:           pr.GetBase().GetSHA(),
		HeadRef:           pr.GetHead().GetRef(),
		HeadSHA:           pr.GetHead().GetSHA(),
		IsFork:            isFork,
		AuthorAssociation: pr.GetAuthorAssociation(),
	}, nil
}

func (g *GitHub) ListFiles(ctx context.Context, r Repo, number int) ([]File, error) {
	var out []File
	opts := &github.ListOptions{PerPage: 100}
	for {
		files, resp, err := g.client.PullRequests.ListFiles(ctx, r.Owner, r.Name, number, opts)
		if err != nil {
			return nil, fmt.Errorf("gh: list files %s/%s#%d: %w", r.Owner, r.Name, number, err)
		}
		for _, f := range files {
			out = append(out, File{
				Path:         f.GetFilename(),
				PreviousPath: f.GetPreviousFilename(),
				Status:       f.GetStatus(),
				Additions:    f.GetAdditions(),
				Deletions:    f.GetDeletions(),
				Patch:        f.GetPatch(),
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *GitHub) ListCommits(ctx context.Context, r Repo, number int) ([]string, error) {
	var out []string
	opts := &github.ListOptions{PerPage: 100}
	for {
		commits, resp, err := g.client.PullRequests.ListCommits(ctx, r.Owner, r.Name, number, opts)
		if err != nil {
			return nil, fmt.Errorf("gh: list commits %s/%s#%d: %w", r.Owner, r.Name, number, err)
		}
		for _, c := range commits {
			out = append(out, c.GetCommit().GetMessage())
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *GitHub) FileContent(ctx context.Context, r Repo, path, ref string) ([]byte, error) {
	rc, _, err := g.client.Repositories.DownloadContents(ctx, r.Owner, r.Name, path, &github.RepositoryContentGetOptions{Ref: ref})
	if err != nil {
		return nil, fmt.Errorf("gh: download content %s/%s:%s@%s: %w", r.Owner, r.Name, path, ref, err)
	}
	defer func() { _ = rc.Close() }()
	// Bound the read to one byte past ContentStore's cap so callers can
	// detect an over-cap file without buffering it in full.
	data, err := io.ReadAll(io.LimitReader(rc, MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("gh: read content %s/%s:%s@%s: %w", r.Owner, r.Name, path, ref, err)
	}
	return data, nil
}

func (g *GitHub) ListIssueComments(ctx context.Context, r Repo, number int) ([]Comment, error) {
	var out []Comment
	opts := &github.IssueListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.Issues.ListComments(ctx, r.Owner, r.Name, number, opts)
		if err != nil {
			return nil, fmt.Errorf("gh: list issue comments %s/%s#%d: %w", r.Owner, r.Name, number, err)
		}
		for _, c := range comments {
			out = append(out, Comment{
				ID:     c.GetID(),
				Body:   c.GetBody(),
				UserID: c.GetUser().GetID(),
				IsBot:  c.GetUser().GetType() == "Bot",
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *GitHub) ListReviewComments(ctx context.Context, r Repo, number int) ([]Comment, error) {
	var out []Comment
	opts := &github.PullRequestListCommentsOptions{ListOptions: github.ListOptions{PerPage: 100}}
	for {
		comments, resp, err := g.client.PullRequests.ListComments(ctx, r.Owner, r.Name, number, opts)
		if err != nil {
			return nil, fmt.Errorf("gh: list review comments %s/%s#%d: %w", r.Owner, r.Name, number, err)
		}
		for _, c := range comments {
			out = append(out, Comment{
				ID:     c.GetID(),
				Body:   c.GetBody(),
				UserID: c.GetUser().GetID(),
				IsBot:  c.GetUser().GetType() == "Bot",
			})
		}
		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}
	return out, nil
}

func (g *GitHub) CreateIssueComment(ctx context.Context, r Repo, number int, body string) (int64, error) {
	c, _, err := g.client.Issues.CreateComment(ctx, r.Owner, r.Name, number, &github.IssueComment{Body: github.Ptr(body)})
	if err != nil {
		return 0, fmt.Errorf("gh: create issue comment %s/%s#%d: %w", r.Owner, r.Name, number, err)
	}
	return c.GetID(), nil
}

func (g *GitHub) EditIssueComment(ctx context.Context, r Repo, id int64, body string) error {
	_, _, err := g.client.Issues.EditComment(ctx, r.Owner, r.Name, id, &github.IssueComment{Body: github.Ptr(body)})
	if err != nil {
		return fmt.Errorf("gh: edit issue comment %s/%s#%d: %w", r.Owner, r.Name, id, err)
	}
	return nil
}

func (g *GitHub) ReactIssueComment(ctx context.Context, r Repo, id int64, content string) error {
	_, _, err := g.client.Reactions.CreateIssueCommentReaction(ctx, r.Owner, r.Name, id, content)
	if err != nil {
		return fmt.Errorf("gh: react issue comment %s/%s#%d: %w", r.Owner, r.Name, id, err)
	}
	return nil
}

func (g *GitHub) CreateReview(ctx context.Context, r Repo, number int, headSHA, body string, comments []ReviewComment) error {
	draft := make([]*github.DraftReviewComment, 0, len(comments))
	for _, c := range comments {
		dc := &github.DraftReviewComment{
			Path: github.Ptr(c.Path),
			Body: github.Ptr(c.Body),
			Side: github.Ptr(c.Side),
			Line: github.Ptr(c.Line),
		}
		if c.StartLine != 0 {
			dc.StartLine = github.Ptr(c.StartLine)
			dc.StartSide = github.Ptr(c.Side)
		}
		draft = append(draft, dc)
	}
	_, _, err := g.client.PullRequests.CreateReview(ctx, r.Owner, r.Name, number, &github.PullRequestReviewRequest{
		CommitID: github.Ptr(headSHA),
		Body:     github.Ptr(body),
		Event:    github.Ptr("COMMENT"),
		Comments: draft,
	})
	if err != nil {
		return fmt.Errorf("gh: create review %s/%s#%d: %w", r.Owner, r.Name, number, err)
	}
	return nil
}

func (g *GitHub) CreateFileComment(ctx context.Context, r Repo, number int, headSHA, path, body string) error {
	_, _, err := g.client.PullRequests.CreateComment(ctx, r.Owner, r.Name, number, &github.PullRequestComment{
		Path:        github.Ptr(path),
		Body:        github.Ptr(body),
		CommitID:    github.Ptr(headSHA),
		SubjectType: github.Ptr("FILE"),
	})
	if err != nil {
		return fmt.Errorf("gh: create file comment %s/%s#%d %s: %w", r.Owner, r.Name, number, path, err)
	}
	return nil
}

func (g *GitHub) PermissionLevel(ctx context.Context, r Repo, user string) (string, error) {
	level, _, err := g.client.Repositories.GetPermissionLevel(ctx, r.Owner, r.Name, user)
	if err != nil {
		return "", fmt.Errorf("gh: permission level %s/%s %s: %w", r.Owner, r.Name, user, err)
	}
	return level.GetPermission(), nil
}
