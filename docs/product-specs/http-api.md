---
title: Worker HTTP API
status: drafted
last-reviewed: 2026-04-26
---

# Worker HTTP API

Public HTTP TCP surface for job submission. Default listen `:8081`. The
shell is the only intended client.

## VOD

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/video/transcode` | `VODSubmitRequest` | one-shot encode |
| POST | `/v1/video/transcode/status` | `{ job_id }` | poll job |
| GET  | `/v1/video/transcode/presets` | — | list configured presets |

## ABR

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/video/transcode/abr` | `ABRSubmitRequest` | per-rendition encode + master HLS |
| POST | `/v1/video/transcode/abr/status` | `{ job_id }` | poll job |

## Live (RTMP-based; new shape per plan 0002)

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/stream/start` | `StreamStartRequest` (work_id + preset + optional payment_ticket; no Channel fields) | operator pre-registration; broadcaster connects to returned `rtmp_url` |
| POST | `/stream/stop` | `{ work_id }` | graceful close |
| POST | `/stream/topup` | `{ work_id, payment_ticket }` | extend session balance |
| POST | `/stream/status` | `{ work_id }` | snapshot stream state |

## Health + diagnostics

| Method | Path | Notes |
|---|---|---|
| GET | `/healthz` | liveness + mode + active stream count |
| GET | `/capabilities` | mirror of capability strings advertised to service-registry, plus `public_rtmp` URL and `max_concurrent` |

## Auth

Optional bearer token via `Authorization: Bearer <token>` if
`--auth-token=<token>` is set. Independent of payment ticket validation.

Paid endpoints additionally require a base64-encoded `payment_ticket`
field in the request body, which the worker validates against
`payment-daemon` via `ProcessPayment`. Live mode's `/stream/start` and
`/stream/topup` paths debit the streaming-session balance via
`OpenStreamingSession` / `TopUpStreamingSession`. Worker-side
implementation lives in `internal/service/liverunner/`.

## Error envelope

```
{
  "code": "<short-camel-case-code>",
  "message": "<human readable>"
}
```

Common codes: `bad_json`, `missing_fields`, `wrong_mode`, `no_runner`,
`unauthorized`, `payment`, `bad_ticket`, plus `types.ErrCode*` (e.g.,
`JOB_INVALID_PRESET`, `STREAM_NOT_FOUND`, `INVALID_PAYMENT`,
`INSUFFICIENT_BALANCE`, `TOPUP_RATE_LIMITED`).

## Cross-references

- [`grpc-surface.md`](grpc-surface.md) — operator gRPC over unix socket
- [`../design-docs/live-rtmp-protocol.md`](../design-docs/live-rtmp-protocol.md)
- [`../../README.md`](../../README.md)
