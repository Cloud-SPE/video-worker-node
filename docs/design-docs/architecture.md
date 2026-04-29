---
title: Worker module architecture
status: drafted
last-reviewed: 2026-04-26
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

## Per-mode wiring (compile-time)

The cmd entry inspects `--mode=` and constructs only the runner needed:

```go
switch {
case cfg.Mode.IsVOD():
    jobR, err = jobrunner.New(jobrunner.Config{ ... })
case cfg.Mode.IsABR():
    abrR, err = abrrunner.New(abrrunner.Config{ ... })
case cfg.Mode.IsLive():
    liveR, err = liverunner.New(liverunner.Config{ ... })
}
```

Each runner carries its own minimal dependency set (FFmpeg, storage,
webhooks, payment, presets, GPU profile). No runner depends on another.

The HTTP layer rejects mismatched paths with `501 Not Implemented`:

- VOD path called when `--mode=abr` → 501.
- Live path called when `--mode=vod` → 501.
- Same vice-versa.

A future `--mode=multi` (out of MVP scope) would wire all three
runners. Tracked in this module's tech-debt-tracker if/when needed.

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
