package gh

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
)

const (
	// MaxFileBytes is the per-file content cap. Over cap, the file is
	// treated as unreadable: evidence against it cannot be verified, so
	// groundedness fails and the finding is dropped.
	MaxFileBytes = 1 << 20
	// MaxTotalContentBytes is the per-run cap across every distinct
	// path@ref fetched.
	MaxTotalContentBytes = 32 << 20
)

// ErrFileTooLarge is returned when a file (or the running per-run total)
// exceeds its byte cap.
var ErrFileTooLarge = errors.New("gh: file too large")

// ErrBinaryFile is returned when the first 8000 bytes of a file contain a
// NUL byte.
var ErrBinaryFile = errors.New("gh: binary file")

// ContentStore wraps Port.FileContent with an in-memory cache keyed by
// "path@ref" and enforces MaxFileBytes / MaxTotalContentBytes so a run
// cannot be made to buffer unbounded content across personas that share the
// same head/base refs.
type ContentStore struct {
	port Port
	repo Repo

	mu    sync.Mutex
	cache map[string]cacheEntry
	total int
}

type cacheEntry struct {
	data []byte
	err  error
}

// NewContentStore builds a ContentStore over port for a single repository.
func NewContentStore(port Port, repo Repo) *ContentStore {
	return &ContentStore{port: port, repo: repo, cache: make(map[string]cacheEntry)}
}

// Get returns the content of path at ref, from cache if already fetched this
// run. A cached failure (over-cap, binary) is replayed rather than retried,
// since neither condition changes within a run.
func (s *ContentStore) Get(ctx context.Context, path, ref string) ([]byte, error) {
	key := path + "@" + ref

	s.mu.Lock()
	if e, ok := s.cache[key]; ok {
		s.mu.Unlock()
		return e.data, e.err
	}
	s.mu.Unlock()

	data, err := s.port.FileContent(ctx, s.repo, path, ref)
	if err == nil {
		switch {
		case len(data) > MaxFileBytes:
			data, err = nil, fmt.Errorf("%w: %s@%s", ErrFileTooLarge, path, ref)
		case isBinary(data):
			data, err = nil, fmt.Errorf("%w: %s@%s", ErrBinaryFile, path, ref)
		}
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if err == nil {
		if s.total+len(data) > MaxTotalContentBytes {
			err = fmt.Errorf("%w: %s@%s exceeds the run's MaxTotalContentBytes budget", ErrFileTooLarge, path, ref)
			data = nil
		} else {
			s.total += len(data)
		}
	}
	s.cache[key] = cacheEntry{data: data, err: err}
	return data, err
}

func isBinary(data []byte) bool {
	n := min(len(data), 8000)
	return bytes.IndexByte(data[:n], 0) >= 0
}
