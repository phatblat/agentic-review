// Package ghevent parses GitHub webhook payloads into agentic-review's own
// Event shape and implements the coarse, API-free pre-work skip checks from
// spec §11.1/§11.2. It never calls the GitHub API itself; Gate's
// priorSummary argument and the marker-based summons skip-fast (spec §11.2)
// are computed by the caller, which has access to a Port and to
// internal/render's marker parser.
package ghevent

import (
	"fmt"
	"strings"

	"github.com/google/go-github/v75/github"

	"github.com/phatblat/agentic-review/internal/gh"
)

// Kind identifies which webhook fired.
type Kind string

const (
	KindPullRequest  Kind = "PullRequest"
	KindIssueComment Kind = "IssueComment"
)

// Event is the subset of a GitHub webhook payload agentic-review acts on.
type Event struct {
	Kind   Kind
	Action string
	Repo   gh.Repo

	// Populated for Kind == KindPullRequest.
	PRNumber int
	PRDraft  bool
	// BaseChanged is action == "edited" && payload.changes.base != nil — the
	// retarget signal in spec §11.1.
	BaseChanged bool

	// Populated for Kind == KindIssueComment.
	CommentID          int64
	CommentBody        string
	CommentAuthor      string
	CommentAuthorIsBot bool
	// IsPullRequest is true when the issue_comment fired on a pull request
	// rather than a plain issue.
	IsPullRequest bool
}

// Trigger returns the run-history trigger string for ev: one of "opened",
// "ready_for_review", "reopened", "edited", "retarget", or "/agentic-review".
func (ev *Event) Trigger() string {
	if ev.Kind == KindIssueComment {
		return "/agentic-review"
	}
	if ev.Action == "edited" && ev.BaseChanged {
		return "retarget"
	}
	return ev.Action
}

// Parse decodes a GitHub webhook payload into an Event. eventName is the
// X-GitHub-Event header value (equivalently, $GITHUB_EVENT_NAME). Only
// pull_request and issue_comment are supported.
func Parse(eventName string, payload []byte) (*Event, error) {
	raw, err := github.ParseWebHook(eventName, payload)
	if err != nil {
		return nil, fmt.Errorf("ghevent: parse %s webhook: %w", eventName, err)
	}

	switch e := raw.(type) {
	case *github.PullRequestEvent:
		return &Event{
			Kind:        KindPullRequest,
			Action:      e.GetAction(),
			Repo:        repoOf(e.GetRepo()),
			PRNumber:    e.GetNumber(),
			PRDraft:     e.GetPullRequest().GetDraft(),
			BaseChanged: e.GetAction() == "edited" && e.GetChanges().GetBase() != nil,
		}, nil
	case *github.IssueCommentEvent:
		return &Event{
			Kind:               KindIssueComment,
			Action:             e.GetAction(),
			Repo:               repoOf(e.GetRepo()),
			PRNumber:           e.GetIssue().GetNumber(),
			CommentID:          e.GetComment().GetID(),
			CommentBody:        e.GetComment().GetBody(),
			CommentAuthor:      e.GetComment().GetUser().GetLogin(),
			CommentAuthorIsBot: e.GetComment().GetUser().GetType() == "Bot",
			IsPullRequest:      e.GetIssue().GetPullRequestLinks() != nil,
		}, nil
	default:
		return nil, fmt.Errorf("ghevent: unsupported event %q (%T)", eventName, raw)
	}
}

func repoOf(r *github.Repository) gh.Repo {
	return gh.Repo{Owner: r.GetOwner().GetLogin(), Name: r.GetName()}
}

// Gate implements the coarse spec §11.1/§11.2 pre-work skip checks:
//   - a draft PR at "opened" skips ("ready_for_review" catches it later);
//   - "edited"/"reopened" skip when a prior kind=summary marker exists and
//     the base branch did not change (review-once, spec §11.1);
//   - "ready_for_review" always proceeds;
//   - an issue_comment skips unless it is on a pull request, from a
//     non-bot author, and mentions "/agentic-review" (spec §11.2).
//
// priorSummary is precomputed by the caller (it requires listing existing
// issue comments), so Gate makes no API calls and has no side effects.
func Gate(ev *Event, priorSummary bool) (proceed bool, reason string) {
	switch ev.Kind {
	case KindPullRequest:
		switch ev.Action {
		case "opened":
			if ev.PRDraft {
				return false, "draft pull request"
			}
			return true, ""
		case "edited", "reopened":
			if priorSummary && !ev.BaseChanged {
				return false, "already reviewed; no retarget"
			}
			return true, ""
		case "ready_for_review":
			return true, ""
		default:
			return false, fmt.Sprintf("unhandled pull_request action %q", ev.Action)
		}
	case KindIssueComment:
		if ev.Action != "created" {
			return false, fmt.Sprintf("unhandled issue_comment action %q", ev.Action)
		}
		if !ev.IsPullRequest {
			return false, "comment is not on a pull request"
		}
		if ev.CommentAuthorIsBot {
			return false, "comment author is a bot"
		}
		if !strings.Contains(ev.CommentBody, "/agentic-review") {
			return false, "comment does not mention /agentic-review"
		}
		return true, ""
	default:
		return false, fmt.Sprintf("unknown event kind %q", ev.Kind)
	}
}
