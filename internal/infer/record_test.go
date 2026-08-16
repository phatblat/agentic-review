package infer

import (
	"context"
	"errors"
	"os"
	"testing"
)

type fakeClient struct {
	resp *Response
	err  error
	n    int
}

func (f *fakeClient) Complete(context.Context, string, *Request) (*Response, error) {
	f.n++
	return f.resp, f.err
}

func TestSelectPrefersReplayDirOverRecordDir(t *testing.T) {
	got := Select(&fakeClient{}, "/replay", "/record", "logic")
	replayer, ok := got.(*Replayer)
	if !ok {
		t.Fatalf("Select returned %T, want *Replayer when ReplayDir is set", got)
	}
	if replayer.dir != "/replay" || replayer.personaID != "logic" {
		t.Errorf("replayer = %+v, want dir=/replay personaID=logic", replayer)
	}
}

func TestSelectUsesRecordDirWhenNoReplayDir(t *testing.T) {
	base := &fakeClient{}
	got := Select(base, "", "/record", "logic")
	recorder, ok := got.(*Recorder)
	if !ok {
		t.Fatalf("Select returned %T, want *Recorder", got)
	}
	if recorder.dir != "/record" || recorder.personaID != "logic" || recorder.inner != base {
		t.Errorf("recorder = %+v, want dir=/record personaID=logic wrapping base", recorder)
	}
}

func TestSelectReturnsBaseWhenNeitherSet(t *testing.T) {
	base := &fakeClient{}
	got := Select(base, "", "", "logic")
	if got != Client(base) {
		t.Errorf("Select = %v, want the base client unwrapped", got)
	}
}
func TestRecorderThenReplayerRoundTrip(t *testing.T) {
	dir := t.TempDir()
	inner := &fakeClient{resp: &Response{Choices: []Choice{{Message: Message{Role: "assistant", Content: "hello"}}}, Usage: Usage{TotalTokens: 7}}}
	rec := NewRecorder(dir, "verifier/groundedness", inner)

	req := &Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Seed: 99}
	resp, err := rec.Complete(context.Background(), "http://x/v1", req)
	if err != nil {
		t.Fatalf("Recorder.Complete: %v", err)
	}
	if resp.Choices[0].Message.Content != "hello" {
		t.Fatalf("resp = %+v", resp)
	}
	if inner.n != 1 {
		t.Fatalf("inner.n = %d, want 1", inner.n)
	}

	replay := NewReplayer(dir, "verifier/groundedness")
	// A different Seed must still hit the same recording (seed is excluded
	// from the key).
	req2 := &Request{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}, Seed: 1}
	got, err := replay.Complete(context.Background(), "http://x/v1", req2)
	if err != nil {
		t.Fatalf("Replayer.Complete: %v", err)
	}
	if got.Choices[0].Message.Content != "hello" || got.Usage.TotalTokens != 7 {
		t.Errorf("replayed resp = %+v", got)
	}
}

func TestRecorderPersonaIDSlashesEscaped(t *testing.T) {
	dir := t.TempDir()
	inner := &fakeClient{resp: &Response{}}
	rec := NewRecorder(dir, "verifier/groundedness", inner)
	req := &Request{Model: "m"}
	if _, err := rec.Complete(context.Background(), "http://x", req); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	path, err := recordingPath(dir, "verifier/groundedness", "http://x", req)
	if err != nil {
		t.Fatalf("recordingPath: %v", err)
	}
	if _, statErr := os.Stat(path); statErr != nil {
		t.Fatalf("recording not written at expected path %s: %v", path, statErr)
	}
}

func TestReplayerMissReturnsErrNoRecording(t *testing.T) {
	dir := t.TempDir()
	replay := NewReplayer(dir, "logic")
	_, err := replay.Complete(context.Background(), "http://x", &Request{Model: "m"})
	if !errors.Is(err, ErrNoRecording) {
		t.Fatalf("err = %v, want ErrNoRecording", err)
	}
}

func TestRecordingKeyExcludesSeedAndTimeouts(t *testing.T) {
	req1 := &Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}, Seed: 1, MaxTokens: 100}
	req2 := &Request{Model: "m", Messages: []Message{{Role: "user", Content: "x"}}, Seed: 99, MaxTokens: 999}
	k1, err := recordingKey("http://x", req1)
	if err != nil {
		t.Fatalf("recordingKey: %v", err)
	}
	k2, err := recordingKey("http://x", req2)
	if err != nil {
		t.Fatalf("recordingKey: %v", err)
	}
	if k1 != k2 {
		t.Errorf("k1=%s k2=%s, want equal (seed/max_tokens excluded from the key)", k1, k2)
	}
}

func TestRecordingKeyDiffersOnModel(t *testing.T) {
	req1 := &Request{Model: "a"}
	req2 := &Request{Model: "b"}
	k1, _ := recordingKey("http://x", req1)
	k2, _ := recordingKey("http://x", req2)
	if k1 == k2 {
		t.Errorf("k1 == k2 == %s, want different keys for different models", k1)
	}
}
