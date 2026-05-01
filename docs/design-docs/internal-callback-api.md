---
title: Internal callback API (worker → shell)
status: accepted
last-reviewed: 2026-04-26
---

# Internal callback API (worker → shell)

The shell exposes a small HTTP surface that **only** the worker is
expected to call. It's how the worker reports stream-lifecycle events
back to the shell so customer webhooks fire and balance accounting
stays correct. This API is **not** part of the engine package — it's a
pure shell concern that lives next to the gateway and isn't exposed to
customers.

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

### `/internal/live/session-active`

Worker reports first FFmpeg-produced output, opens the streaming
reservation. Shell flips `media.live_streams.status` to `active`, sets
`worker_url` and `last_seen_at`, inserts a `streaming` row in
`app.reservations`, fires `video.live_stream.active`.

```
Request:  { stream_id: string, worker_url: string, started_at?: string (RFC3339) }
Response: { ok: true, reservation_id: string }
```

### `/internal/live/session-tick`

Worker calls every `debit_cadence` (5s default) with the encoded-seconds
delta. Shell decrements project balance, returns runway and grace
status. Idempotent on `seq`: replaying the same `seq` returns the
current state without double-debiting.

```
Request:  { stream_id: string, seq: int, debit_seconds: float, cumulative_seconds: float }
Response: { ok: true, balance_cents: int, runway_seconds: int, grace_triggered: bool }
```

When `runway_seconds < runway_low_threshold` (30s default), the shell
also fires `video.live_stream.runway_low`. `grace_triggered=true` means
balance hit zero — worker should call `/topup` and / or start its grace
deadline.

### `/internal/live/session-ended`

Worker reports stream end + reason. Shell commits the streaming
reservation with the final usage report, transitions the row to
`recording_processing` (if `recording_enabled=true`) or `ended`, and
fires `video.live_stream.ended`.

```
Request:  { stream_id: string, reason: 'graceful' | 'insufficient_balance' | 'session_worker_failed' | 'admin_stop',
            final_seq: int, final_seconds: float }
Response: { ok: true, recording_processing: bool }
```

If `recording_processing=true`, the worker should follow up with
`/recording-finalized` once its `livecdn.Mirror` has flushed the final
scan.

### `/internal/live/recording-finalized`

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

### `/internal/live/topup`

Worker requests authorization for an N-second runway top-up. Shell
checks the project's prepaid balance: if sufficient, returns
`succeeded:true` + the cents amount the worker should debit-credit
against its co-located payment-daemon receiver. If insufficient, fires
`video.live_stream.topup_failed` and returns `succeeded:false`.

```
Request:  { stream_id: string, request_seconds: int }
Response: { succeeded: bool, authorized_cents: int, balance_cents: int }
```

The actual `TopUpStreamingSession` gRPC call against payment-daemon
happens worker-side after `succeeded:true`.

## Error codes

| HTTP | When |
|---|---|
| `200` | success |
| `400` | malformed payload (zod parse failed) |
| `401` | missing / wrong shared secret |
| `404` | unknown `stream_id` |
| `409` | state-mismatch (e.g., `/session-tick` on a non-active stream, `/recording-finalized` on a stream not in `recording_processing`) |
| `500` | unexpected service error |

The HTTP layer is in `apps/api/src/runtime/http/internal/live/index.ts`;
business logic in `apps/api/src/service/live/liveSessionService.ts`. The
worker-side client is `apps/transcode-worker-node/internal/providers/shellclient`.

## Cross-references

- [`live-state-machine.md`](live-state-machine.md) — which transitions each callback maps to.
- [`streaming-session-pattern.md`](streaming-session-pattern.md) — payment side of `/session-tick` and `/topup`.
- [`recording-bridge.md`](recording-bridge.md) — what `/recording-finalized` triggers.
- [`ports-and-trust-boundaries.md`](ports-and-trust-boundaries.md) — auth model context.
- Plan 0006 §B + §F — implementation plan.
