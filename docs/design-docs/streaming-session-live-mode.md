---
title: Streaming-session payment in live mode
status: drafted
last-reviewed: 2026-04-26
---

# Streaming-session payment in live mode

> **Status**: drafted. The full live runner (RTMP pipe + streaming-session
> payment + recording bridge) is implemented in `internal/service/liverunner/`.
> This doc captures the design; the code is the source of truth.

## What the worker does

| Step | Worker call | payment-daemon API |
|---|---|---|
| Session open | once, on RTMP accept | `OpenStreamingSession({ stream_id, capability, initial_credit_seconds: 60 })` |
| Tick | every `DebitCadence` (default 5s) | `DebitStreamingSession({ session_id, seq: monotonic, debit_seconds: delta })` |
| Top-up request | when runway < `RunwaySeconds` (default 30) | (request to shell `/internal/live/topup`; shell calls `TopUpStreamingSession`) |
| Session close | once, on stream end | `CloseStreamingSession({ session_id, final_seq, reason })` |

`seq` is monotonically increasing per session, persisted in BoltDB so a
worker restart resumes correct sequencing. payment-daemon idempotently
rejects duplicate `seq` values (covers worker retry on transient gRPC
failure).

## Knobs (Config fields, defaulted in `liverunner.New`)

| Field | Default | Meaning |
|---|---|---|
| `DebitCadence` | 5s | Time between `DebitStreamingSession` calls |
| `RunwaySeconds` | 30 | If `remaining_runway_seconds` from the tick response drops below this, request topup |
| `GraceSeconds` | 60 | If runway hits 0 and no topup succeeds, allow this much grace before close |
| `PreCreditSeconds` | 60 | Initial credit at OpenStreamingSession |
| `DebitRetryBackoff` | 1s | Backoff between transient debit retries |
| `RestartLimit` | 3 | Max FFmpeg restarts before fatal close |
| `TopupMinInterval` | 5s | Rate-limit between topup requests |

## Why the runner owns the loop, not payment-daemon

payment-daemon provides the **primitive** (open/debit/topup/close); the
runner owns the **policy** (cadence, runway, grace, restart-on-FFmpeg-crash).
This split is canonical to `livepeer-modules`'s pattern; we
inherit it intact.

## Cross-references

- [`streaming-session-pattern.md`](streaming-session-pattern.md) — the generalized pattern
- [`live-state-machine.md`](live-state-machine.md) — live session state transitions
- [`payment-integration.md`](payment-integration.md) — `ProcessPayment` + `DebitBalance` pipeline
- `livepeer-modules/payment-daemon/docs/design-docs/streaming-session-pattern.md` — the daemon's own spec
