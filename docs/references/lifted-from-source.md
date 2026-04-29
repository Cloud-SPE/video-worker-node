---
title: Provenance — files lifted from livepeer-modules/transcode-worker-node
status: accepted
last-reviewed: 2026-04-29
---

# Provenance

Code in this repo was originally lifted from
`livepeer-modules/transcode-worker-node` (sister repo) at SHA
`41204aa`. The source repo is left untouched at runtime; this repo has
no Go-import dependency on it.

> This file documents only the lift from `livepeer-modules` (proto stubs
> + the original worker shape). Per [exec-plan 0001](../exec-plans/active/0001-extract-from-platform.md)
> decision **D3**, this repo deliberately does **not** track provenance from
> `livepeer-video-platform`. If you are looking for "where did this code come
> from in the platform monorepo," that is intentionally not recorded.

## Differences from source

The lift is faithful with the following deliberate deltas:

1. **Module path**: `github.com/Cloud-SPE/livepeer-modules/transcode-worker-node` → `github.com/Cloud-SPE/video-worker-node` swept across all `.go`, `.proto`, and config files.
2. **Binary name**: `livepeer-transcode-worker-node` → `livepeer-video-worker-node`.
3. **Metric prefix**: `livepeer_transcode_*` → `livepeer_videoworker_*` per [`docs/conventions/metrics.md`](../conventions/metrics.md).
4. **Webhook signature header**: `X-Webhook-Signature` → `X-Video-Signature` per [`docs/conventions/webhook-signing.md`](../conventions/webhook-signing.md).
5. **Removed**: `internal/providers/trickle/` and the source's `internal/service/liverunner/`. Trickle is replaced with native RTMP ingest via a new `internal/providers/ingest/` interface; `liverunner` is rewritten against that interface.
6. **Added**: `internal/providers/ingest/` (new interface + `rtmp/` impl using `github.com/yutopp/go-rtmp`).
7. **Added**: `internal/service/liverunner/` (rewritten — RTMP-based; FFmpeg pipe; HLS output; streaming-session payment).
8. **Lints**: this repo's `lint/` set adds `no-chain-commons/`. The other lints (`no-cgo`, `layer-check`, `no-secrets-in-logs`, `coverage-gate`) are lifted with module-path adjustments.
9. **Docs**: pillar docs (`AGENTS.md`, `DESIGN.md`, `PRODUCT_SENSE.md`, `PLANS.md`, `README.md`) rewritten for this standalone repo; design-doc `live-trickle-protocol.md` REPLACED by [`../design-docs/live-rtmp-protocol.md`](../design-docs/live-rtmp-protocol.md).

## What was lifted unchanged (modulo the deltas above)

| Path | Source path |
|---|---|
| `internal/types/` | `internal/types/` |
| `internal/config/` | `internal/config/` |
| `internal/repo/jobs/` | `internal/repo/jobs/` |
| `internal/providers/{ffmpeg,gpu,probe,storage,store,hls,thumbnails,filters,progress,clock,logger,metrics,webhooks,paymentclient,registryclient}/` | same paths in source |
| `internal/service/{jobrunner,abrrunner,capabilityreporter,preflight,paymentbroker,presetloader}/` | same paths in source |
| `internal/runtime/{grpc,lifecycle,metrics}/` | same paths in source |
| `proto/clients/livepeer/{payments,registry}/v1/` | same paths in source |
| `presets/h264-{vod,abr,live}.yaml` | derived from `presets/h264-streaming.yaml` and `presets/abr-standard.yaml` |
| `codecs-builder/` | `codecs-builder/` |
| `Dockerfile` | `Dockerfile` (binary name + go version updated) |

## Why we copy rather than depend

- We don't want a cross-repo Go import dependency on a sibling repo.
- We need to make worker-specific changes (metric prefix, webhook header, Trickle removal, ingest abstraction) without negotiating PRs against the source.
- The source repo continues to exist; nothing here tries to deprecate it.
