package gh

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-github/v75/github"
)

// request is one HTTP request as it arrived on the wire.
type request struct {
	line string // "METHOD /path"
	body map[string]any
}

var (
	validSides        = []string{"RIGHT", "LEFT"}
	validSubjectTypes = []string{"line", "file"}
)

// newCaptureServer returns a server that records the single request it
// receives and, like the real REST API, answers 422 when an enum field
// carries a value outside its documented set: side/start_side (top level or
// inside review comments[]) must be RIGHT|LEFT and subject_type must be
// line|file. A fake Port cannot reject a bad enum value; this can.
func newCaptureServer(t *testing.T) (*httptest.Server, <-chan request) {
	t.Helper()
	reqs := make(chan request, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v: %s", err, raw)
		}
		reqs <- request{line: r.Method + " " + r.URL.Path, body: body}

		if msg := enumViolation(body); msg != "" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			resp, _ := json.Marshal(map[string]any{"message": "Validation Failed", "errors": []map[string]string{{"message": msg}}})
			_, _ = w.Write(resp)
			return
		}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	t.Cleanup(srv.Close)
	return srv, reqs
}

// enumViolation returns a description of the first enum field in body that
// the real API would reject, or "" when every enum field is valid.
func enumViolation(body map[string]any) string {
	check := func(obj map[string]any, field string, valid []string) string {
		v, ok := obj[field]
		if !ok {
			return ""
		}
		if s, isStr := v.(string); isStr && slices.Contains(valid, s) {
			return ""
		}
		return field + " " + toJSON(v) + " is not a member of " + toJSON(valid)
	}
	if msg := check(body, "subject_type", validSubjectTypes); msg != "" {
		return msg
	}
	objs := []map[string]any{body}
	if comments, ok := body["comments"].([]any); ok {
		for _, c := range comments {
			if m, ok := c.(map[string]any); ok {
				objs = append(objs, m)
			}
		}
	}
	for _, o := range objs {
		for _, f := range []string{"side", "start_side"} {
			if msg := check(o, f, validSides); msg != "" {
				return msg
			}
		}
	}
	return ""
}

func toJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// newTestGitHub returns a GitHub whose client talks to srv instead of
// api.github.com.
func newTestGitHub(t *testing.T, srv *httptest.Server) *GitHub {
	t.Helper()
	u, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}
	c := github.NewClient(srv.Client())
	c.BaseURL = u
	return &GitHub{client: c}
}

// Regression: CreateFileComment sent subject_type "FILE", which the REST
// API rejects ("FILE is not a member of [\"line\", \"file\"]", 422), so a
// run whose surviving findings included a file-anchored one (config-guard's
// blockers) failed to post anything. Asserting on the wire body, not a
// mock's argument, is what catches this: a fake Port cannot reject a bad
// enum value.
func TestCreateFileCommentSendsLowercaseSubjectType(t *testing.T) {
	srv, reqs := newCaptureServer(t)
	g := newTestGitHub(t, srv)
	repo := Repo{Owner: "acme", Name: "demo"}
	if err := g.CreateFileComment(context.Background(), repo, 7, "abc123", ".github/agentic-review/config.yaml", "body"); err != nil {
		t.Fatalf("CreateFileComment: %v", err)
	}

	req := <-reqs
	if want := "POST /repos/acme/demo/pulls/7/comments"; req.line != want {
		t.Errorf("request = %q, want %q", req.line, want)
	}
	// Exact comparison also proves a file-level comment carries no line.
	want := map[string]any{
		"path":         ".github/agentic-review/config.yaml",
		"commit_id":    "abc123",
		"body":         "body",
		"subject_type": "file",
	}
	if diff := cmp.Diff(want, req.body); diff != "" {
		t.Errorf("request body mismatch (-want +got):\n%s", diff)
	}
}

// CreateReview maps gh.ReviewComment onto the REST review-comment shape.
// The wire body is asserted exactly (so an unexpected start_line/start_side
// on a single-line comment fails too); numbers decode as float64.
func TestCreateReviewWireBody(t *testing.T) {
	tests := []struct {
		name     string
		comments []ReviewComment
		want     []any
	}{
		{
			name:     "single-line RIGHT",
			comments: []ReviewComment{{Path: "a.go", Line: 10, Side: "RIGHT", Body: "b1"}},
			want: []any{
				map[string]any{"path": "a.go", "body": "b1", "side": "RIGHT", "line": float64(10)},
			},
		},
		{
			name:     "single-line LEFT",
			comments: []ReviewComment{{Path: "a.go", Line: 4, Side: "LEFT", Body: "b2"}},
			want: []any{
				map[string]any{"path": "a.go", "body": "b2", "side": "LEFT", "line": float64(4)},
			},
		},
		{
			name:     "multi-line RIGHT",
			comments: []ReviewComment{{Path: "a.go", Line: 12, StartLine: 9, Side: "RIGHT", Body: "b3"}},
			want: []any{
				map[string]any{
					"path": "a.go", "body": "b3", "side": "RIGHT", "line": float64(12),
					"start_line": float64(9), "start_side": "RIGHT",
				},
			},
		},
		{
			name:     "multi-line LEFT",
			comments: []ReviewComment{{Path: "a.go", Line: 8, StartLine: 5, Side: "LEFT", Body: "b4"}},
			want: []any{
				map[string]any{
					"path": "a.go", "body": "b4", "side": "LEFT", "line": float64(8),
					"start_line": float64(5), "start_side": "LEFT",
				},
			},
		},
		{
			name: "batched mix",
			comments: []ReviewComment{
				{Path: "a.go", Line: 1, Side: "RIGHT", Body: "x"},
				{Path: "b.go", Line: 6, StartLine: 2, Side: "LEFT", Body: "y"},
			},
			want: []any{
				map[string]any{"path": "a.go", "body": "x", "side": "RIGHT", "line": float64(1)},
				map[string]any{
					"path": "b.go", "body": "y", "side": "LEFT", "line": float64(6),
					"start_line": float64(2), "start_side": "LEFT",
				},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, reqs := newCaptureServer(t)
			g := newTestGitHub(t, srv)
			repo := Repo{Owner: "acme", Name: "demo"}
			if err := g.CreateReview(context.Background(), repo, 7, "abc123", "", tc.comments); err != nil {
				t.Fatalf("CreateReview: %v", err)
			}

			req := <-reqs
			if want := "POST /repos/acme/demo/pulls/7/reviews"; req.line != want {
				t.Errorf("request = %q, want %q", req.line, want)
			}
			want := map[string]any{
				"commit_id": "abc123",
				"body":      "",
				"event":     "COMMENT",
				"comments":  tc.want,
			}
			if diff := cmp.Diff(want, req.body); diff != "" {
				t.Errorf("request body mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// A side outside {RIGHT, LEFT} is rejected by the API with a 422; the error
// must surface to the caller with the status intact rather than being
// swallowed, so a bad mapping upstream fails the run loudly.
func TestCreateReviewSurfacesValidationError(t *testing.T) {
	for _, side := range []string{"right", "BOTH", ""} {
		t.Run("side="+side, func(t *testing.T) {
			srv, reqs := newCaptureServer(t)
			g := newTestGitHub(t, srv)
			repo := Repo{Owner: "acme", Name: "demo"}
			err := g.CreateReview(context.Background(), repo, 7, "abc123", "", []ReviewComment{
				{Path: "a.go", Line: 3, Side: side, Body: "b"},
			})
			if err == nil {
				t.Fatal("CreateReview succeeded, want a 422 for an invalid side")
			}
			var ger *github.ErrorResponse
			if !errors.As(err, &ger) || ger.Response.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("err = %v, want a wrapped *github.ErrorResponse with status 422", err)
			}
			if got := (<-reqs).body["comments"]; got == nil {
				t.Error("server never saw the review comments")
			}
		})
	}
}
