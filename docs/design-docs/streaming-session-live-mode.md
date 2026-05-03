---
title: Streaming-session payment in live mode
status: drafted
last-reviewed: 2026-05-03
---

# Streaming-session payment in live mode

> **Status**: drafted. The full live runner (RTMP pipe + streaming-session
> payment + recording bridge) is implemented in `internal/service/liverunner/`.
> This doc captures the design; the code is the source of truth.
>
> **Transition note**: the table below previously described the older
> `OpenStreamingSession` / `DebitStreamingSession` framing. The worker is
> now moving to Pattern B:
>
> - gateway opens via `POST /api/sessions/start`
> - worker chooses `work_id`, opens `OpenSession(...)`, then applies the
>   first `ProcessPayment(...)`
> - worker debits locally with `DebitBalance(...)`
> - worker checks runway locally with `SufficientBalance(...)`
> - gateway funds further runtime through canonical topups
> - worker emits typed lifecycle/accounting events to
>   `POST /internal/live/events`

## What the worker does

| Step | Worker call | payment-daemon API |
|---|---|---|
| Session open | once, before RTMP accept | gateway `POST /api/sessions/start`; worker `OpenSession(...)` then `ProcessPayment(...)` |
| Tick | every `DebitCadence` (default 5s) | worker `DebitBalance({ sender, work_id, seq, units })` |
| Runway check | after each debit | worker `SufficientBalance({ sender, work_id, min_units })` |
| Top-up request | when gateway decides funding should continue | gateway `POST /api/sessions/{gateway_session_id}/topup`; worker `ProcessPayment(...)` |
| Session close | once, on stream end | worker emits `session.ended`; worker `CloseSession({ sender, work_id })` |

`seq` is monotonically increasing per session, persisted in BoltDB so a
worker restart resumes correct sequencing. The current implementation is
still fail-fast on restart for live sessions, but the persisted counter
and correlation fields are now shaped for future recovery work.

## Knobs (Config fields, defaulted in `liverunner.New`)

| Field | Default | Meaning |
|---|---|---|
| `DebitCadence` | 5s | Time between local debit attempts |
| `RunwaySeconds` | 30 | Minimum runway target checked locally by the worker |
| `GraceSeconds` | 60 | If runway stays insufficient, allow this much grace before close |
| `PreCreditSeconds` | 60 | Initial runtime funding target at session open |
| `DebitRetryBackoff` | 1s | Backoff between transient debit retries |
| `RestartLimit` | 3 | Max FFmpeg restarts before fatal close |
| `TopupMinInterval` | 5s | Rate-limit between topup requests |

## Why the runner owns the loop, not payment-daemon

payment-daemon provides the **primitive** (process payment, debit,
balance check, close); the runner owns the **policy** (cadence, runway,
grace, restart-on-FFmpeg-crash). This split remains canonical; what has
changed is that the worker, not the shell, is now the runtime authority.

## Cross-references

- [`streaming-session-pattern.md`](streaming-session-pattern.md) — the generalized pattern
- [`live-state-machine.md`](live-state-machine.md) — legacy shell-state reference
- [`payment-integration.md`](payment-integration.md) — `ProcessPayment` + `DebitBalance` pipeline
- `livepeer-modules/payment-daemon/docs/design-docs/streaming-session-pattern.md` — the daemon's own spec
