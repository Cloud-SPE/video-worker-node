---
title: Ingest provider interface
status: accepted
last-reviewed: 2026-04-26
---

# `internal/providers/ingest/` interface

Wire-protocol-agnostic abstraction over live broadcaster ingest. RTMP at
MVP; SRT and WHIP/WebRTC are tech-debt items.

## Why an interface, not direct RTMP wiring

Two reasons:

1. **The live runner code is wire-protocol-agnostic.** Adding SRT or WHIP
   later means adding a new `providers/ingest/<protocol>/` impl, not
   rewriting `liverunner`.
2. **A future "dedicated ingest gateway" service can carve this same
   provider out into its own process** without rewriting the worker. The
   gateway would import `providers/ingest/` and implement `SessionAcceptor`
   to forward to remote workers. No worker rewrite required.

## The interface

```go
type IngestProvider interface {
    Protocol() Protocol
    Listen(ctx context.Context, acceptor SessionAcceptor) error
    Stop(ctx context.Context) error
}

type IngestSession interface {
    Protocol() Protocol
    StreamKey() string
    MediaFormat() string                // "flv" for RTMP, "ts" for SRT, ...
    Reader() io.Reader
    RemoteAddr() string
    Close() error
}

type SessionAcceptor interface {
    Accept(ctx context.Context, sess IngestSession) (Acceptance, error)
}

type Acceptance struct {
    OnEnd func(reason string)
}
```

## Why `SessionAcceptor` is a callback, not a channel

Acceptance is a synchronous decision: validate the stream key with the
shell, open a payment session, decide `accept` or `reject` before any
media bytes flow. A callback model keeps that decision close to the
provider (no buffering / queueing) and lets the provider reject quickly
on capacity / invalid-key without spinning up downstream resources.

`OnEnd` lets the acceptor's caller (typically `liverunner`) react to
session termination — close the FFmpeg pipe, finalize the streaming
session with payment-daemon, hand off to the recording bridge.

## RTMP impl (`providers/ingest/rtmp/`)

- Uses `github.com/yutopp/go-rtmp` (BSD-2-Clause; pure Go; no cgo).
- Stream key extracted from `cmd.PublishingName` in the OnPublish callback.
- Session bytes piped through `io.Pipe()` (writer fed by RTMP audio/video chunks; reader exposed to FFmpeg).
- Stop() calls `rtmp.Server.Close()` (necessary to unblock Serve; closing the bare listener isn't enough — go-rtmp absorbs Accept errors).
- `MediaFormat()` returns `"flv"` (RTMP wraps in FLV).

## Backlog impls

- **SRT** (`providers/ingest/srt/`) — alternative live ingest. `gortsplib`-style
  pure-Go libs exist but vetting is needed. Will plug in via the same interface.
- **WHIP / WebRTC** (`providers/ingest/whip/`) — significant. Adds a
  paired SFU integration (LiveKit / OvenMediaEngine / mediasoup TBD) and
  WHEP playback bridge. Cross-component because it requires SFU
  coordination with the consuming shell.

## Cross-references

- [`live-rtmp-protocol.md`](live-rtmp-protocol.md) — RTMP-specific details
- [`../exec-plans/tech-debt-tracker.md`](../exec-plans/tech-debt-tracker.md) — SRT / WHIP / Intel-AMD-live entries
