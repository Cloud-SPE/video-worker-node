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
| POST | `/v1/video/transcode` | `VODSubmitRequest` (`preset`, optional `offering`) | one-shot encode; paid via `livepeer-payment` header |
| POST | `/v1/video/transcode/status` | `{ job_id }` | poll job |
| GET  | `/v1/video/transcode/presets` | — | list configured presets |

## ABR

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/v1/video/transcode/abr` | `ABRSubmitRequest` (`presets`, optional `offering`) | per-rendition encode + master HLS; paid via `livepeer-payment` header |
| POST | `/v1/video/transcode/abr/status` | `{ job_id }` | poll job |

## Live (RTMP-based; Pattern B session surface)

| Method | Path | Body | Notes |
|---|---|---|---|
| POST | `/api/sessions/start` | `SessionStartRequest` (`gateway_session_id`, optional `preset`, optional `offering`) | canonical worker session-open route; requires `livepeer-payment` header; opens a pending payee session, credits receiver-side balance, and returns `worker_session_id` + `work_id` |
| POST | `/api/sessions/{gateway_session_id}/topup` | payment only | canonical worker session-topup route; requires `livepeer-payment` header; applies credit to the existing live `work_id` |
| POST | `/api/sessions/{gateway_session_id}/end` | — | canonical graceful end route; closes the receiver-side payment session and stops the worker-owned live session |
| POST | `/stream/start` | `StreamStartRequest` (`stream_id` or `work_id`, `preset`, optional `offering`; no Channel fields) | legacy worker live route retained during the rewrite; now opens/credits the payee session before registering the stream |
| POST | `/stream/stop` | `{ stream_id }` | legacy worker live route retained during the rewrite |
| POST | `/stream/topup` | `{ stream_id }` + payment | legacy worker live route retained during the rewrite |
| POST | `/stream/status` | `{ stream_id }` | legacy worker live route retained during the rewrite; returns the persisted `types.Stream` record, including Pattern B correlation/runtime fields such as `gateway_session_id`, `worker_session_id`, `payment_work_id`, `low_balance`, and `grace_until` when present |

## Health + diagnostics

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | liveness + mode + active stream count + GPU scheduler snapshot |
| GET | `/registry/offerings` | suite-wide capability advertisement for orch-coordinator scrape; omits internal `backend_url`, may include orch-internal `worker_eth_address` |
| POST | `/v1/payment/ticket-params` | authenticated helper that proxies receiver-side `GetTicketParams` for exact `(sender, recipient, face_value, capability, offering)` lookups |

## Auth

Optional bearer token via `Authorization: Bearer <token>` when
top-level `auth_token` is configured in shared `worker.yaml`. In the
v3.0.1 contract this gates `GET /registry/offerings` and
`POST /v1/payment/ticket-params`; payment ticket validation on paid
work routes is separate.

Paid VOD / ABR endpoints require a base64-encoded `livepeer-payment`
header carrying `livepeer.payments.v1.Payment` bytes. The worker first
opens an authoritative payee session using its configured capability /
offering catalog, then validates the payment against `payment-daemon`
via `ProcessPayment`.

Canonical live session routes use the `livepeer-payment` header and do
not require a JSON `payment_ticket` alias. During the worker rewrite,
the older `/stream/start` and `/stream/topup` routes still tolerate body
`payment_ticket` while callers migrate. Worker-side implementation lives
in `internal/service/liverunner/` plus the HTTP session route layer in
`internal/runtime/http/`.

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

Common codes: `bad_json`, `missing_fields`, `no_runner`,
`unauthorized`, `payment`, `bad_ticket`, plus `types.ErrCode*` (e.g.,
`JOB_INVALID_PRESET`, `STREAM_NOT_FOUND`, `INVALID_PAYMENT`,
`INSUFFICIENT_BALANCE`, `TOPUP_RATE_LIMITED`).

## Cross-references

- [`grpc-surface.md`](grpc-surface.md) — operator gRPC over unix socket
- [`../design-docs/live-rtmp-protocol.md`](../design-docs/live-rtmp-protocol.md)
- [`../../README.md`](../../README.md)
