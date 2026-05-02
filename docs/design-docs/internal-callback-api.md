---
title: Internal callback API (worker → shell)
status: drafted
last-reviewed: 2026-05-02
---

# Internal callback API (worker → shell)

> **Transition note**: this file exists mainly to explain the shell-side
> integration seam during migration. The worker is now centered on a
> smaller Pattern B surface where:
>
> - `validate-key` remains a separate RTMP admission call
> - authoritative worker lifecycle/accounting events flow through
>   `POST /internal/live/events`
> - legacy `session-active`, `session-tick`, `session-ended`,
>   `recording-finalized`, and `topup` remain only as transitional
>   compatibility while the gateway rewrite is in flight

The shell exposes a small HTTP surface that **only** the worker is
expected to call. This API is **not** part of the engine package — it's
a pure shell concern that lives next to the gateway and isn't exposed
to customers.

For the current worker direction, the most important contract is the
typed event-ingest endpoint:

### `/internal/live/events`

Worker posts canonical Pattern B events here. The event body carries
`gateway_session_id` plus, when available:

- `worker_session_id`
- `work_id`
- `usage_seq`
- `units`
- `unit_type`
- `remaining_runway_units`
- `low_balance`
- `reason`
- `final_units`
- recording-finalization fields

Current worker event types include:

- `session.ready`
- `session.usage.tick`
- `session.balance.low`
- `session.balance.refilled`
- `session.error`
- `session.ended`
- `session.recording.ready`

The remaining sections in this document describe the older callback
taxonomy that still exists only on the fallback path.

## Auth model

Every request carries a header:

```
X-Worker-Secret: <WORKER_SHELL_SECRET>
```

The shell rejects with `401` if the header is missing or doesn't match
the configured secret. The secret is operator-supplied (one per
deployment) and lives in env on both sides:

- Shell: `apps/api/src/config/env.ts` → `WORKER_SHELL_SECRET`.
- Worker: `--shell-internal-secret` flag → `internal/config.Config.ShellInternalSecret` → `providers/shellclient.Config.Secret`.

mTLS upgrade is tracked as tech-debt; for MVP, shared-secret over a
private network (or VPC peering) is sufficient.

## Endpoints

All routes are `POST` under `/internal/live/`. Request and response
bodies are JSON. snake_case on the wire, camelCase in service code.

### `/internal/live/validate-key`

Worker calls when an RTMP publish arrives. Shell linear-scans candidate
`media.live_streams` rows in `idle | active | reconnecting`, runs
`hasher.verify()` against each, and returns the first match. Linear
scan is the same compromise `apiKeyAuthResolver` makes (salted hashes
aren't queryable directly).

```
Request:  { stream_key: string, worker_url: string }
Response: { accepted: bool, stream_id?: string, project_id?: string, recording_enabled?: bool }
```

`accepted=false` is the expected response for unknown / wrong / already-ended keys.

### Legacy `/internal/live/session-active`

Worker reports first FFmpeg-produced output, opens the streaming
reservation. Shell flips `media.live_streams.status` to `active`, sets
`worker_url` and `last_seen_at`, inserts a `streaming` row in
`app.reservations`, fires `video.live_stream.active`.

```
Request:  { stream_id: string, worker_url: string, started_at?: string (RFC3339) }
Response: { ok: true, reservation_id: string }
```

### Legacy `/internal/live/session-tick`

Worker calls every `debit_cadence` (5s default) with the encoded-seconds
delta. Shell decrements project balance, returns runway and grace
status. Idempotent on `seq`: replaying the same `seq` returns the
current state without double-debiting.

```
Request:  { stream_id: string, seq: int, debit_seconds: float, cumulative_seconds: float }
Response: { ok: true, balance_cents: int, runway_seconds: int, grace_triggered: bool }
```

In Pattern B, the worker no longer relies on this response to decide
runtime continuation. Local receiver-side runway remains authoritative.

### Legacy `/internal/live/session-ended`

Worker reports stream end + reason. Shell commits the streaming
reservation with the final usage report, transitions the row to
`recording_processing` (if `recording_enabled=true`) or `ended`, and
fires `video.live_stream.ended`.

```
Request:  { stream_id: string, reason: 'graceful' | 'insufficient_balance' | 'session_worker_failed' | 'admin_stop',
            final_seq: int, final_seconds: float }
Response: { ok: true, recording_processing: bool }
```

In the current Pattern B direction, the worker instead emits
`session.recording.ready` after final segment state is known.

### Legacy `/internal/live/recording-finalized`

Worker reports the cumulative segment list once the bridge can take
over. Shell delegates to `recordingFinalizer.finalize()`: server-side
copies each segment + variant under the new asset prefix, inserts
rendition rows, builds the master manifest, and transitions the asset
to `ready`. Then flips the live-stream row to `recording_ready` and
fires both `video.live_stream.recording_ready` AND `video.asset.ready`.

```
Request:  { stream_id: string,
            segment_storage_keys: string[],
            master_storage_key: string,
            total_duration_sec: float }
Response: { ok: true, recording_asset_id: string }
```

### Legacy `/internal/live/topup`

Worker requests authorization for an N-second runway top-up. Shell
checks the project's prepaid balance: if sufficient, returns
`succeeded:true` + the cents amount the worker should debit-credit
against its co-located payment-daemon receiver. If insufficient, fires
`video.live_stream.topup_failed` and returns `succeeded:false`.

```
Request:  { stream_id: string, request_seconds: int }
Response: { succeeded: bool, authorized_cents: int, balance_cents: int }
```

The actual receiver-side topup/debit loop remains worker-side. In the
current Pattern B direction, shells are moving toward minting payment
and forwarding it to the worker’s canonical `/api/sessions/{gateway_session_id}/topup`
route instead of authorizing a separate shell callback loop.

## Error codes

| HTTP | When |
|---|---|
| `200` | success |
| `400` | malformed payload (zod parse failed) |
| `401` | missing / wrong shared secret |
| `404` | unknown `stream_id` |
| `409` | state mismatch on the legacy callback flow |
| `500` | unexpected service error |

The worker-side client is `internal/providers/shellclient`.

## Cross-references

- [`live-state-machine.md`](live-state-machine.md) — legacy shell-state reference.
- [`streaming-session-pattern.md`](streaming-session-pattern.md) — current worker-side live payment direction.
- [`recording-bridge.md`](recording-bridge.md) — what `/recording-finalized` triggers.
- [`ports-and-trust-boundaries.md`](ports-and-trust-boundaries.md) — auth model context.
- Plan 0006 §B + §F — implementation plan.
