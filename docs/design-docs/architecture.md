---
title: Worker module architecture
status: drafted
last-reviewed: 2026-05-03
---

# Worker module architecture

See [`../../DESIGN.md`](../../DESIGN.md) for the layer diagram and per-mode
wiring. This doc captures the module-internal architectural decisions that
inform the diagram.

## Layer enforcement

Two enforcement mechanisms:

1. **`.golangci.yml` depguard rules** — declarative per-package import
   bans. Catches the bulk of layer violations and the cross-cutting
   single-hop boundaries (`os/exec`, `prometheus/client_golang`,
   `go-rtmp`, `chain-commons`).
2. **Custom Go lints under `lint/`** — `no-cgo`, `no-chain-commons`,
   `no-secrets-in-logs`, plus stubs for `layer-check` and `doc-gardener`
   to consolidate when depguard isn't expressive enough.

## Unified runner wiring

The canonical worker architecture wires all three runners in one
process:

- `jobrunner` for VOD
- `abrrunner` for ABR
- `liverunner` for live

Each runner still carries its own minimal dependency set (FFmpeg,
storage, webhooks, payment, presets, GPU profile), but the cmd entry no
longer treats them as mutually exclusive. The architectural boundary is
now:

- one worker process
- one HTTP surface
- one RTMP ingest surface
- one shared GPU scheduler

The HTTP layer should route based on runner availability and scheduler
admission, not a process-wide mode switch. The old `501 wrong_mode`
behavior is no longer the target architecture.

## Scheduler boundary

The shared GPU scheduler belongs behind a provider boundary and is
consumed by all three runners. First-cut scheduling is slot-based with
static preset-derived cost weighting:

- batch queueing
- live reserved headroom
- fail-fast live overload
- operator overrides on top of detected GPU profile
- preset-derived workload cost as a secondary admission gate

The scheduler API should remain vendor-neutral even though NVIDIA,
Intel, and AMD execution paths differ underneath.

## Why the worker has no `runtime/dispatch/` layer

A consuming shell typically has a `dispatch/` layer to orchestrate
framework-free over an adapter set. The worker's "orchestration" is the
runner itself (jobrunner / abrrunner / liverunner) — there's no value in
interposing a separate dispatch layer because the runner already owns
the orchestration. The worker's `runtime/http/` is a thin wrapper that
maps HTTP → runner.

## Cross-references

- [`ingest-interface.md`](ingest-interface.md)
- [`live-rtmp-protocol.md`](live-rtmp-protocol.md)
- [`streaming-session-live-mode.md`](streaming-session-live-mode.md)
- [`subprocess-vs-embed.md`](subprocess-vs-embed.md)
