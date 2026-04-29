---
title: Worker module design-docs index
status: drafted
last-reviewed: 2026-04-26
---

# Worker design-docs

| Doc | Status | Summary |
|---|---|---|
| [architecture.md](architecture.md) | drafted | Module-internal layer diagram + per-mode wiring |
| [ingest-interface.md](ingest-interface.md) | accepted | `providers/ingest/` abstraction; gateway-carve-out future |
| [live-rtmp-protocol.md](live-rtmp-protocol.md) | drafted | RTMP-based live ingest (replaces the source's live-trickle-protocol) |
| [streaming-session-live-mode.md](streaming-session-live-mode.md) | drafted | How live mode integrates the streaming-session payment pattern |
| [gpu-vendor-strategy.md](gpu-vendor-strategy.md) | drafted | NVIDIA / Intel / AMD detection + per-vendor Docker builds |
| [subprocess-vs-embed.md](subprocess-vs-embed.md) | accepted | Why FFmpeg is a subprocess, not a cgo binding |

Cross-component design-docs lifted from the consuming-platform context (the worker's part of them):

| Doc | Status | Summary |
|---|---|---|
| [internal-callback-api.md](internal-callback-api.md) | accepted | Worker → shell callback API for live session events |
| [live-state-machine.md](live-state-machine.md) | accepted | Live session state transitions |
| [recording-bridge.md](recording-bridge.md) | accepted | Live → VOD finalization handoff |
| [payment-integration.md](payment-integration.md) | accepted | `ProcessPayment` + `DebitBalance` pipeline |
| [streaming-session-pattern.md](streaming-session-pattern.md) | accepted | Generalized streaming-session payment pattern |
| [worker-discovery.md](worker-discovery.md) | accepted | Capability registration + refresh |
