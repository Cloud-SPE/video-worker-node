---
title: Live RTMP protocol (replaces source's live-trickle-protocol)
status: drafted
last-reviewed: 2026-04-26
---

# Live RTMP protocol

> **Status**: drafted. Implementation lives in
> `internal/providers/ingest/rtmp/` (RTMP server, stream-key extraction,
> SessionAcceptor) and `internal/service/liverunner/` (state machine + payment loop).
> The code is the source of truth; this doc captures the design rationale.

## What this replaces

`livepeer-modules/transcode-worker-node/docs/design-docs/live-trickle-protocol.md`
described a Trickle-channel-based live workflow where the gateway POSTed
`subscribe_url` + `publish_url` for the worker to pull/push segments
through. We replace that entirely with native RTMP at MVP because:

- Trickle's stability issues were the documented blocker for Intel/AMD
  live encode in the source repo's tech-debt tracker.
- RTMP is the broadcaster default. OBS, FFmpeg's CLI, every cloud
  encoder speaks RTMP out of the box.
- We get a simpler customer-facing model: customer creates a
  `LiveStream` and gets back `rtmp://ingest:1935/live/{stream_key}`.

## Wire flow (the new model)

```
broadcaster (OBS / FFmpeg)
  │
  │ RTMP publish: rtmp://host:1935/live/{stream_key}
  ▼
worker providers/ingest/rtmp/
  │
  │ extract stream_key from URL path
  │ invoke SessionAcceptor.Accept(sess)
  │
  ├──▶ shell /internal/live/validate-key  (HTTP)
  │      ↑ shell hashes stream_key, looks up media.live_streams.stream_key_hash
  │      ↑ returns { accepted: true, stream_id, project_id, ... } or rejects
  │
  ├──▶ payment-daemon.OpenStreamingSession (gRPC unix)
  │      ↑ pre-credits 60s of runway
  │
  ▼
liverunner spawns FFmpeg with input = pipe:0 (FLV from ingest session)
  │
  │ FFmpeg writes HLS segments + variant playlists to storage provider
  │ (signed PUT URLs to MinIO/S3/R2)
  ▼
HLS playable at the storage prefix; shell's playback redirect serves it
```

## Stream key validation timing

On RTMP handshake, before FFmpeg spawn. Fail fast if invalid; no wasted
encode work, no payment debits for un-keyed streams.

## What changes from the source's live HTTP surface

The source's `POST /stream/start` had `subscribe_url` + `publish_url` +
`channel` fields (Trickle Channel info). The new shape removes them
entirely:

```json
{
  "work_id":     "stream-xyz",
  "preset":      "h264-live",
  "webhook_url": "https://op.example.com/webhooks/worker",
  "webhook_secret": "...",
  "payment_ticket": "..."
}
```

Response includes the public `rtmp_url` so the operator can pre-register
streams (advanced use case). Most flows skip `/stream/start` entirely:
the broadcaster just connects to the public RTMP URL and the worker
validates inline.

## Recording bridge integration

When the broadcaster disconnects or the runner force-closes:
- FFmpeg subprocess exits cleanly (or is SIGTERM/SIGKILL'd).
- Worker calls shell `/internal/live/recording-finalized` with the segment list.
- Shell creates a `media.assets` row and runs the recording bridge.

Detail: [`recording-bridge.md`](recording-bridge.md).

## Cross-references

- [`ingest-interface.md`](ingest-interface.md) — the abstraction the RTMP impl satisfies
- [`streaming-session-live-mode.md`](streaming-session-live-mode.md) — payment-side details
- [`internal-callback-api.md`](internal-callback-api.md) — the worker → shell callback API
- [`live-state-machine.md`](live-state-machine.md) — live session state transitions
