package livecdn

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// LocalFSSink writes to a local directory (e.g., a Docker volume shared
// with the playback-origin nginx). Default for compose-based deployments;
// production replaces this with an S3 sink (tracked as tech-debt).
type LocalFSSink struct {
	Root string
}

// NewLocalFSSink constructs a sink rooted at `root`. Creates the dir if
// missing.
func NewLocalFSSink(root string) (*LocalFSSink, error) {
	if root == "" {
		return nil, errors.New("livecdn.LocalFSSink: empty root")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, err
	}
	return &LocalFSSink{Root: root}, nil
}

// Put writes the body to a file under Root keyed by `key`.
func (s *LocalFSSink) Put(_ context.Context, key, _ string, body io.Reader) error {
	abs := filepath.Join(s.Root, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	tmp := abs + ".tmp"
	f, err := os.Create(tmp) //nolint:gosec
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, abs)
}

// Delete removes the file at `key`. No-op if missing.
func (s *LocalFSSink) Delete(_ context.Context, key string) error {
	abs := filepath.Join(s.Root, filepath.FromSlash(key))
	err := os.Remove(abs)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// InMemorySink is a Sink for tests. Records all Put/Delete operations
// and stores the latest body bytes per key.
type InMemorySink struct {
	mu      sync.Mutex
	objects map[string][]byte
	puts    []string
	deletes []string
}

// NewInMemorySink returns an empty sink.
func NewInMemorySink() *InMemorySink {
	return &InMemorySink{objects: map[string][]byte{}}
}

// Put records the upload + stores the bytes.
func (s *InMemorySink) Put(_ context.Context, key, _ string, body io.Reader) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.objects[key] = b
	s.puts = append(s.puts, key)
	s.mu.Unlock()
	return nil
}

// Delete records the delete + removes the bytes.
func (s *InMemorySink) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	delete(s.objects, key)
	s.deletes = append(s.deletes, key)
	s.mu.Unlock()
	return nil
}

// Object returns the latest stored bytes for `key`, or nil if absent.
func (s *InMemorySink) Object(key string) []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	if b, ok := s.objects[key]; ok {
		return append([]byte(nil), b...)
	}
	return nil
}

// Keys returns a snapshot of all currently-stored keys.
func (s *InMemorySink) Keys() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, 0, len(s.objects))
	for k := range s.objects {
		out = append(out, k)
	}
	return out
}

// PutLog returns the chronological list of Put calls.
func (s *InMemorySink) PutLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.puts))
	copy(out, s.puts)
	return out
}

// DeleteLog returns the chronological list of Delete calls.
func (s *InMemorySink) DeleteLog() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.deletes))
	copy(out, s.deletes)
	return out
}
