package gh

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/google/go-github/v75/github"
)

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
	type captured struct {
		path string
		body map[string]any
	}
	reqs := make(chan captured, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("read request body: %v", err)
		}
		var body map[string]any
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Errorf("request body is not JSON: %v: %s", err, raw)
		}
		reqs <- captured{path: r.Method + " " + r.URL.Path, body: body}
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":1}`))
	}))
	defer srv.Close()

	g := newTestGitHub(t, srv)
	repo := Repo{Owner: "acme", Name: "demo"}
	if err := g.CreateFileComment(context.Background(), repo, 7, "abc123", ".github/agentic-review/config.yaml", "body"); err != nil {
		t.Fatalf("CreateFileComment: %v", err)
	}

	req := <-reqs
	gotPath, got := req.path, req.body
	if want := "POST /repos/acme/demo/pulls/7/comments"; gotPath != want {
		t.Errorf("request = %q, want %q", gotPath, want)
	}
	if st := got["subject_type"]; st != "file" {
		t.Errorf("subject_type = %v, want \"file\" (the REST enum is lowercase)", st)
	}
	for key, want := range map[string]string{
		"path": ".github/agentic-review/config.yaml", "commit_id": "abc123", "body": "body",
	} {
		if got[key] != want {
			t.Errorf("%s = %v, want %q", key, got[key], want)
		}
	}
	if _, ok := got["line"]; ok {
		t.Errorf("body carries a line (%v); a file-level comment must not", got["line"])
	}
}
