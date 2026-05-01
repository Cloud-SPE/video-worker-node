// Package livecdn mirrors the FFmpeg live-encode output directory to a
// storage Sink: new segments + updated playlists get pushed; segments
// FFmpeg has rotated out (via `delete_segments`) get deleted from the
// sink so the storage footprint matches the rolling DVR window.
//
// The sink boundary lets us swap a local-fs target (compose harness +
// shared volume + nginx playback origin) for an S3 / object-storage
// uploader without changing the runner.
package livecdn

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Sink is the storage target the live mirror writes segments + playlists
// to. Operations are keyed by the relative path under the stream's
// storage prefix (e.g., "h264/720p/segment_00001.ts").
type Sink interface {
	// Put uploads or overwrites the object at `key` with `body`.
	Put(ctx context.Context, key, contentType string, body io.Reader) error
	// Delete removes the object at `key`. No-op if missing.
	Delete(ctx context.Context, key string) error
}

// Mirror watches a local directory and replicates its contents to the
// Sink. Designed to track FFmpeg's `-hls_flags delete_segments+...`
// behavior: every file that exists locally must exist in the sink, and
// every file FFmpeg has unlinked must be deleted from the sink.
type Mirror struct {
	// LocalRoot is the directory FFmpeg writes into.
	LocalRoot string
	// SinkPrefix is prepended to every key Put into the Sink.
	SinkPrefix string
	// Sink is the upload target.
	Sink Sink
	// PollInterval governs how often the local directory is scanned.
	// Default: 500ms.
	PollInterval time.Duration

	mu       sync.Mutex
	known    map[string]int64 // local relpath → last-seen size
	segments []string         // every segment ever uploaded (recording-bridge input)
	logErr   func(err error)
}

// NewMirror constructs a Mirror. `Run` blocks until ctx is cancelled.
func NewMirror(localRoot, sinkPrefix string, sink Sink) *Mirror {
	return &Mirror{
		LocalRoot:    localRoot,
		SinkPrefix:   strings.TrimRight(sinkPrefix, "/"),
		Sink:         sink,
		PollInterval: 500 * time.Millisecond,
		known:        map[string]int64{},
	}
}

// SetLogger attaches a logger callback for non-fatal errors during the
// scan loop. Optional; missing logger swallows errors silently.
func (m *Mirror) SetLogger(fn func(err error)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.logErr = fn
}

// Run polls the LocalRoot until ctx is cancelled. On exit, returns nil.
// Per-tick errors are surfaced via the optional logger.
func (m *Mirror) Run(ctx context.Context) error {
	if m.LocalRoot == "" {
		return errors.New("livecdn.Mirror: empty LocalRoot")
	}
	if m.Sink == nil {
		return errors.New("livecdn.Mirror: nil Sink")
	}
	t := time.NewTicker(m.PollInterval)
	defer t.Stop()
	for {
		if err := m.scanOnce(ctx); err != nil {
			m.logf(err)
		}
		select {
		case <-ctx.Done():
			// Final scan before returning so any straggler segments
			// land in the sink.
			_ = m.scanOnce(context.Background())
			return nil
		case <-t.C:
		}
	}
}

// ScanOnce is exported for tests: runs a single iteration of the scan
// loop synchronously.
func (m *Mirror) ScanOnce(ctx context.Context) error {
	return m.scanOnce(ctx)
}

func (m *Mirror) scanOnce(ctx context.Context) error {
	current := map[string]int64{}
	err := filepath.Walk(m.LocalRoot, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return nil // tolerate transient stat errors during ffmpeg writes
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(m.LocalRoot, path)
		if err != nil {
			return nil
		}
		current[filepath.ToSlash(rel)] = info.Size()
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("walk: %w", err)
	}

	m.mu.Lock()
	prev := m.known
	m.mu.Unlock()

	// Uploads: any file whose size changed (still being written) or which
	// is new since the last scan. Playlists are uploaded every time their
	// size moves so the sink reflects the rolling DVR window.
	for rel, size := range current {
		if old, ok := prev[rel]; ok && old == size && !isPlaylist(rel) {
			continue
		}
		if err := m.uploadOne(ctx, rel); err != nil {
			m.logf(fmt.Errorf("upload %s: %w", rel, err))
		}
	}

	// Deletions: anything in prev but not in current.
	for rel := range prev {
		if _, ok := current[rel]; ok {
			continue
		}
		key := m.keyFor(rel)
		if err := m.Sink.Delete(ctx, key); err != nil {
			m.logf(fmt.Errorf("delete %s: %w", key, err))
		}
	}

	m.mu.Lock()
	m.known = current
	m.mu.Unlock()
	return nil
}

func (m *Mirror) uploadOne(ctx context.Context, rel string) error {
	abs := filepath.Join(m.LocalRoot, filepath.FromSlash(rel))
	f, err := os.Open(abs) //nolint:gosec
	if err != nil {
		return err
	}
	defer f.Close()
	key := m.keyFor(rel)
	ct := contentTypeFor(rel)
	if err := m.Sink.Put(ctx, key, ct, f); err != nil {
		return err
	}
	if isSegment(rel) {
		m.mu.Lock()
		m.segments = append(m.segments, key)
		m.mu.Unlock()
	}
	return nil
}

// Segments returns the de-duplicated keys of every segment uploaded
// since Mirror construction. The runner hands this list to
// `/internal/live/recording-finalized` for the bridge to VOD.
func (m *Mirror) Segments() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := make(map[string]struct{}, len(m.segments))
	out := make([]string, 0, len(m.segments))
	for _, s := range m.segments {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// MasterKey returns the SinkPrefix-relative key the master manifest
// should be uploaded under.
func (m *Mirror) MasterKey() string {
	return m.keyFor("master.m3u8")
}

func (m *Mirror) keyFor(rel string) string {
	if m.SinkPrefix == "" {
		return rel
	}
	return m.SinkPrefix + "/" + rel
}

func (m *Mirror) logf(err error) {
	m.mu.Lock()
	fn := m.logErr
	m.mu.Unlock()
	if fn != nil {
		fn(err)
	}
}

func isSegment(rel string) bool {
	return strings.HasSuffix(rel, ".ts") || strings.HasSuffix(rel, ".m4s")
}

func isPlaylist(rel string) bool {
	return strings.HasSuffix(rel, ".m3u8")
}

func contentTypeFor(rel string) string {
	switch {
	case strings.HasSuffix(rel, ".m3u8"):
		return "application/vnd.apple.mpegurl"
	case strings.HasSuffix(rel, ".ts"):
		return "video/mp2t"
	case strings.HasSuffix(rel, ".m4s"):
		return "video/iso.segment"
	default:
		return "application/octet-stream"
	}
}
