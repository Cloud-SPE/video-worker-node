---
title: Live stream state machine
status: drafted
last-reviewed: 2026-05-02
---

# Live stream state machine

> **Transition note**: this document is now a high-level state reference,
> not the canonical callback contract. The worker implementation is on
> the Pattern B path under plan 0005:
>
> - canonical worker session routes are `POST /api/sessions/start`,
>   `POST /api/sessions/{gateway_session_id}/topup`, and
>   `POST /api/sessions/{gateway_session_id}/end`
> - worker runtime continuation is decided locally from receiver-side
>   balance, not by shell `/session-tick`
> - worker lifecycle/accounting events are converging on
>   `POST /internal/live/events`
> - recording finalization is a separate post-session event
>
> Treat this file as a simplified lifecycle map. For contract shape,
> prefer [`streaming-session-pattern.md`](streaming-session-pattern.md)
> and [`../product-specs/http-api.md`](../product-specs/http-api.md).

The full lifecycle of `media.live_streams` row, from `dispatchLiveStreamCreate`
through optional recording finalization. Three processes participate in
keeping this consistent: the **shell/gateway** owns the commercial row,
the **worker** drives ingest + encoding, and the **payment-daemon** owns
receiver-side credit.

## State enum

`media.live_streams.status ∈`
- `idle` — created, no broadcaster connected. Stream key valid, RTMP URL public.
- `active` — broadcaster connected, FFmpeg producing segments, streaming-session reservation open.
- `reconnecting` — tick miss or RTMP drop; worker retains state for `reconnect_window_seconds` (default 30s).
- `ended` — broadcaster disconnected gracefully or operator-stopped; `recording_enabled=false`.
- `recording_processing` — broadcaster ended with `recording_enabled=true`; bridge running.
- `recording_ready` — recording bridge completed; the same playback ID now serves the VOD asset.
- `errored` — any failure (worker crash beyond stale window, payment exhaustion past grace, finalize fail).

## Transition diagram

```
                       dispatchLiveStreamCreate
                              │
                              ▼
                            idle ───stale-sweep (rare)───▶ errored
                              │
                              │ worker validate-key accepted
                              │ worker session opened
                              ▼
                ┌────────── active ──────────┐
                │            ▲               │
       event miss│            │ recover     │ broadcaster disconnect
                ▼            │               ▼
          reconnecting ──────┘           (worker → session.ended)
                │                              │
                │ stale-sweep (>90s silent)    │
                ▼                              ▼
              errored ◀── grace expiry ── recording_enabled?
                                            │       │
                                       no   │       │ yes
                                            ▼       ▼
                                          ended   recording_processing
                                                    │
                                                    │ worker → session.recording.ready
                                                    │ (bridge adopts segments)
                                                    ▼
                                                recording_ready
                                                    │
                                                    │ (terminal)
                                                    ▼
```

`recording_ready`, `ended`, and `errored` are terminal.

## Transition triggers

| From → To | Trigger | Owner | Callback / event |
|---|---|---|---|
| (none) → `idle` | `POST /v1/live-streams` | shell | fires `video.live_stream.created` |
| `idle` → `active` | RTMP publish accepted, worker session is open, first readiness event emitted | worker | fires `video.live_stream.active` |
| `reconnecting` → `active` | worker reconnect within window; next successful activity/event | worker | (none — silent recovery) |
| `active` → `reconnecting` | event miss / RTMP drop ≤ `reconnect_window_seconds` | worker | fires `video.live_stream.reconnecting` |
| `active` / `reconnecting` → `errored` | worker silent > `staleAfterSec` (90s) | shell sweeper | fires `video.live_stream.errored` |
| `active` / `reconnecting` → `errored` | grace expired with no successful topup | worker (`session.ended` reason=`insufficient_balance`) | fires `video.live_stream.ended` (reason captured) |
| `active` / `reconnecting` → `errored` | encoder crash / ingest failure on worker | worker (`session.ended` reason=`session_worker_failed`) | fires `video.live_stream.ended` (reason captured) |
| `active` / `reconnecting` → `ended` | broadcaster graceful disconnect, `recording_enabled=false` | worker (`session.ended` reason=`graceful`) | fires `video.live_stream.ended` |
| `active` / `reconnecting` → `recording_processing` | broadcaster graceful disconnect, `recording_enabled=true` | worker (`session.ended`) | fires `video.live_stream.ended` |
| `recording_processing` → `recording_ready` | worker emits `session.recording.ready` | worker | fires `video.live_stream.recording_ready` + `video.asset.ready` |
| any → `errored` | finalize bridge throws | shell (recordingFinalizer) | fires `video.live_stream.errored` |

## Owner of each phase

- **`idle`**: shell owns; worker has no knowledge yet (no validate-key has happened).
- **`active` / `reconnecting`**: worker owns the in-flight session via the per-stream goroutine in `liverunner`; shell/gateway tracks `last_seen_at` from worker events.
- **`recording_processing`**: shell owns (worker is winding down). The stale-stream sweeper has a watchdog effect: if `session.recording.ready` does not arrive within the sweep cutoff, the row eventually transitions to `errored`.
- **`recording_ready`**: shell. The matching `media.assets` row owns playback from here on; the live row is mostly historical.
- **`ended` / `errored`**: terminal. Stale sweep + admin tooling can re-emit webhooks if needed but no further state changes happen.

## Sweep semantics

The shell's `staleStreamSweeper` runs every `pollIntervalMs` (15s default) and
finds rows in `{active, reconnecting}` with `last_seen_at` older than
`staleAfterSec` (90s default). For each it transitions to `errored`,
refunds the open `streaming` reservation, and emits
`video.live_stream.errored` with `reason: 'stale_worker'`. This catches
worker crashes, network partitions, and any other state where the
worker silently goes away.

## Cross-references

- [`streaming-session-pattern.md`](streaming-session-pattern.md) — current worker-side payment direction for live.
- [`internal-callback-api.md`](internal-callback-api.md) — transitional callback reference; canonical worker event-ingest direction is documented there.
- [`recording-bridge.md`](recording-bridge.md) — `recording_processing` → `recording_ready` mechanics.
- Worker-side implementation lives in `internal/service/liverunner/` and `internal/service/livecdn/`.
