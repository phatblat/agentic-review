package infer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/phatblat/agentic-review/internal/logx"
)

// ErrNoRecording is returned by Replayer.Complete on a cache miss —
// deliberately loud, since a silent call-out would defeat the point of
// replay-based tests.
var ErrNoRecording = errors.New("infer: no recording for this request")

// Select builds the Client a persona's turn should use, given the run's
// replay/record configuration: replayDir (if set) takes priority and
// returns a Replayer for personaID, never touching base; otherwise
// recordDir (if set) wraps base in a Recorder for personaID; otherwise
// base itself is returned unwrapped. The two modes are mutually
// exclusive by construction — a run either replays fixtures or records
// new ones, never both.
func Select(base Client, replayDir, recordDir, personaID string) Client {
	if replayDir != "" {
		return NewReplayer(replayDir, personaID)
	}
	if recordDir != "" {
		return NewRecorder(recordDir, personaID, base)
	}
	return base
}

type recordedPair struct {
	Request  *Request  `json:"request"`
	Response *Response `json:"response"`
}

// Recorder wraps a Client, writing every request/response pair to
// <dir>/<persona-id-with-slashes-replaced-by-_>/<key>.json, verbatim with
// no redaction (spec §13.3) — private-repo content must be cleaned
// manually before any recording becomes a public fixture.
type Recorder struct {
	dir       string
	personaID string
	inner     Client
	warned    bool
}

// NewRecorder wraps inner, recording every completion for personaID under
// dir.
func NewRecorder(dir, personaID string, inner Client) *Recorder {
	return &Recorder{dir: dir, personaID: personaID, inner: inner}
}

var _ Client = (*Recorder)(nil)

func (r *Recorder) Complete(ctx context.Context, endpoint string, req *Request) (*Response, error) {
	if !r.warned {
		logx.Warn("infer: recording %s requests to %s verbatim, with no redaction", r.personaID, r.dir)
		r.warned = true
	}
	resp, err := r.inner.Complete(ctx, endpoint, req)
	if err != nil {
		return nil, err
	}
	path, perr := recordingPath(r.dir, r.personaID, endpoint, req)
	if perr != nil {
		return resp, fmt.Errorf("infer: compute recording path: %w", perr)
	}
	if werr := writeRecording(path, req, resp); werr != nil {
		return resp, fmt.Errorf("infer: write recording: %w", werr)
	}
	return resp, nil
}

// Replayer wraps no live Client at all: it returns previously-recorded
// responses, keyed identically to Recorder, and never makes a network
// call.
type Replayer struct {
	dir       string
	personaID string
}

// NewReplayer builds a Replayer reading recordings for personaID from dir.
func NewReplayer(dir, personaID string) *Replayer {
	return &Replayer{dir: dir, personaID: personaID}
}

var _ Client = (*Replayer)(nil)

func (r *Replayer) Complete(_ context.Context, endpoint string, req *Request) (*Response, error) {
	path, err := recordingPath(r.dir, r.personaID, endpoint, req)
	if err != nil {
		return nil, fmt.Errorf("infer: compute recording path: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("%w: %s", ErrNoRecording, path)
		}
		return nil, fmt.Errorf("infer: read recording: %w", err)
	}
	var pair recordedPair
	if err := json.Unmarshal(data, &pair); err != nil {
		return nil, fmt.Errorf("infer: decode recording %s: %w", path, err)
	}
	return pair.Response, nil
}

func recordingPath(dir, personaID, endpoint string, req *Request) (string, error) {
	key, err := recordingKey(endpoint, req)
	if err != nil {
		return "", err
	}
	safeID := strings.ReplaceAll(personaID, "/", "_")
	return filepath.Join(dir, safeID, key+".json"), nil
}

// recordingKey hashes the canonical JSON of exactly {endpoint, model,
// messages, tools, response_format} — seed and timeouts are deliberately
// excluded, since they never change the semantic request.
func recordingKey(endpoint string, req *Request) (string, error) {
	canon := struct {
		Endpoint       string          `json:"endpoint"`
		Model          string          `json:"model"`
		Messages       []Message       `json:"messages"`
		Tools          []Tool          `json:"tools"`
		ResponseFormat *ResponseFormat `json:"response_format"`
	}{
		Endpoint:       endpoint,
		Model:          req.Model,
		Messages:       req.Messages,
		Tools:          req.Tools,
		ResponseFormat: req.ResponseFormat,
	}
	data, err := json.Marshal(canon)
	if err != nil {
		return "", fmt.Errorf("infer: marshal canonical request: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

func writeRecording(path string, req *Request, resp *Response) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(recordedPair{Request: req, Response: resp}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}
