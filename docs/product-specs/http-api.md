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
| POST | `/v1/video/transcode` | `VODSubmitRequest` | one-shot encode; paid via `livepeer-payment` header |
| POST | `/v1/video/transcode/status` | `{ job_id }` | poll job |
| GET  | `/v1/video/transcode/presets` | — | list configured presets |

## ABR

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/video/transcode/abr` | `ABRSubmitRequest` | per-rendition encode + master HLS; paid via `livepeer-payment` header |
| POST | `/v1/video/transcode/abr/status` | `{ job_id }` | poll job |

## Live (RTMP-based; new shape per plan 0002)

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/stream/start` | `StreamStartRequest` (`stream_id` or `work_id`, preset; no Channel fields) | operator pre-registration; accepts `livepeer-payment` header and still tolerates body `payment_ticket` during transition |
| POST | `/stream/stop` | `{ stream_id }` | graceful close; `work_id` also accepted for compatibility |
| POST | `/stream/topup` | `{ stream_id }` + payment | extend session balance; accepts header and body ticket |
| POST | `/stream/status` | `{ stream_id }` | snapshot stream state; `work_id` also accepted for compatibility |

## Health + diagnostics

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | liveness + mode + active stream count |
| GET | `/registry/offerings` | suite-wide capability advertisement for orch-coordinator scrape; omits internal `backend_url`, may include orch-internal `worker_eth_address` |
| POST | `/v1/payment/ticket-params` | authenticated helper that proxies receiver-side `GetTicketParams` for exact `(sender, recipient, face_value, capability, offering)` lookups |

## Auth

Optional bearer token via `Authorization: Bearer <token>` when
top-level `auth_token` is configured in shared `worker.yaml`. In the
v3.0.1 contract this gates `GET /registry/offerings` and
`POST /v1/payment/ticket-params`; payment ticket validation on paid
work routes is separate.

Paid VOD / ABR endpoints require a base64-encoded `livepeer-payment`
header carrying `livepeer.payments.v1.Payment` bytes. The worker
validates that payload against `payment-daemon` via `ProcessPayment`.

Live mode's `/stream/start` and `/stream/topup` paths accept the same
header and still tolerate body `payment_ticket` as a compatibility
alias while callers migrate. Worker-side implementation lives in
`internal/service/liverunner/`.

`POST /v1/payment/ticket-params` is unpaid. It does not size work or
create a payment. The caller supplies the exact `face_value_wei`, and
the worker relays the request to the co-located receiver daemon's
`GetTicketParams` RPC.

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
