// Package ingest abstracts over wire-protocol providers that accept live
// broadcaster sessions (RTMP at MVP; SRT and WHIP/WebRTC are tech-debt
// items in this monorepo).
//
// The split exists so the live runner code is wire-protocol-agnostic and
// so a future "dedicated ingest gateway" service can carve this same
// provider out into its own process without rewriting the runner.
package ingest

import (
	"context"
	"errors"
	"io"
)

// Protocol identifies the wire-protocol an ingest provider handles.
type Protocol string

const (
	ProtocolRTMP Protocol = "rtmp"
	ProtocolSRT  Protocol = "srt"  // backlog
	ProtocolWHIP Protocol = "whip" // backlog (WebRTC)
)

// IngestProvider accepts incoming broadcaster sessions over a single
// wire protocol. Implementations bind a public port and route accepted
// sessions to a SessionAcceptor.
type IngestProvider interface {
	// Protocol identifies the wire protocol this provider handles.
	Protocol() Protocol

	// Listen binds the public port and accepts incoming sessions.
	// Blocks until ctx is cancelled or a fatal listen error occurs.
	Listen(ctx context.Context, acceptor SessionAcceptor) error

	// Stop gracefully shuts down the listener; in-flight sessions get
	// up to drainTimeout to terminate before being force-closed.
	Stop(ctx context.Context) error
}

// IngestSession represents one accepted live session. The wire-format
// of Reader is provider-specific (FLV bytes for RTMP, MPEG-TS for SRT, ...).
// FFmpeg consumes via -f <format> -i pipe:0.
type IngestSession interface {
	// Protocol identifies the wire protocol carrying this session.
	Protocol() Protocol

	// StreamKey is the operator-provided key the broadcaster authenticated
	// with (extracted from the publish URL path: rtmp://host:1935/live/{stream_key}).
	StreamKey() string

	// MediaFormat is the FFmpeg `-f` flag value (e.g., "flv" for RTMP).
	MediaFormat() string

	// Reader is the muxed media stream. Closing the session via Close()
	// terminates this Reader.
	Reader() io.Reader

	// RemoteAddr is the broadcaster's network address (for logging / metrics).
	RemoteAddr() string

	// Close terminates the session.
	Close() error
}

// SessionAcceptor is the per-session callback the ingest provider invokes.
// The acceptor decides whether to accept (returns nil error and an Acceptance)
// or reject (returns an error). Rejections close the session before any
// payload bytes flow.
type SessionAcceptor interface {
	// Accept is invoked once per incoming session, after wire-protocol
	// handshake and stream-key extraction. The returned Acceptance is
	// kept by the provider until the session ends; OnEnd fires when the
	// session terminates (graceful or forced).
	Accept(ctx context.Context, sess IngestSession) (Acceptance, error)
}

// Acceptance is the SessionAcceptor's per-session record returned to the
// IngestProvider. The provider is responsible for invoking OnEnd when the
// session terminates.
type Acceptance struct {
	OnEnd func(reason string)
}

// Common errors implementations may return.
var (
	ErrStreamKeyInvalid = errors.New("ingest: stream key invalid")
	ErrCapacityExceeded = errors.New("ingest: capacity exceeded")
	ErrAlreadyListening = errors.New("ingest: already listening")
	ErrNotListening     = errors.New("ingest: not listening")
)
