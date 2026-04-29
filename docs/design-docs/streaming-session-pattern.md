---
title: Streaming-session payment pattern
status: accepted
last-reviewed: 2026-04-26
---

# Streaming-session payment pattern

How live streams pay over time. Adapted from `livepeer-modules/payment-daemon`'s
streaming-session pattern, with the responsibilities split between the
**worker** (drives the per-session lifecycle), the **payment-daemon**
(receiver tracks credit, sender mints tickets), and the **shell** (knows
the customer's prepaid balance and authorizes top-ups).

## Why this pattern (not VOD-style reserve→commit)

VOD encodes are discrete: the engine reserves up-front, runs the job,
commits the actual usage at the end. Live broadcasts don't fit:

- Total duration is unknown when the stream starts.
- Holding a giant reservation up-front would lock more balance than the
  customer needs.
- A broadcaster shouldn't get cut off the moment their balance dips below
  some threshold — they need a small grace window to top up.

Streaming sessions debit small amounts continuously, with a runway
watermark and a grace period. The platform stops the stream cleanly
when balance can't sustain it.

## The five-step recipe

1. **Open**: at session-active, `OpenStreamingSession({ stream_id, capability, initial_credit_seconds: 60 })` pre-credits 60s of runway. Returns an opaque `session_id`.
2. **Tick**: every 5s (`debit_cadence`), the worker calls `DebitStreamingSession({ session_id, seq, debit_seconds: delta })` with the encoded-seconds delta since the last tick. `seq` is monotonically increasing per session — replays of the same `seq` are receiver-side no-ops.
3. **Watermark**: each tick response carries `remaining_runway_seconds`. If runway < 30s (`stream_runway_seconds`), the worker calls `/internal/live/topup`.
4. **Top-up**: shell looks up the project's prepaid balance; if sufficient, returns `succeeded:true` + an authorized cents amount. The worker then calls `TopUpStreamingSession({ session_id, more_seconds })` against its co-located payment-daemon. If insufficient, shell fires `video.live_stream.topup_failed` and the worker enters grace.
5. **Close**: graceful (broadcaster disconnect): `CloseStreamingSession({ session_id, final_seq, reason: 'graceful' })`. Grace-expiry: same call with `reason: 'insufficient_balance'`. Final reconciliation tick + accounting commit happen on the shell side via `/internal/live/session-ended`.

## Worker / shell / daemon responsibilities

| Concern | Owner | Where |
|---|---|---|
| Per-session state machine | worker | `apps/transcode-worker-node/internal/service/liverunner` |
| `seq` counter persistence | worker | BoltDB via `repo/jobs.IncrementDebitSeq`; survives worker restart |
| OpenStreamingSession / DebitStreamingSession / TopUpStreamingSession / CloseStreamingSession gRPC | worker | calls into co-located `payment-daemon` (receiver) |
| Project balance lookup + topup authorization | shell | `/internal/live/topup` |
| Per-second cost basis (`liveCentsPerSecond`) | shell | engine `PricingConfig` |
| Reservation row lifecycle (`streaming` → `committed`/`refunded`) | shell | `service/live/postgresAdapters.createPostgresReservationOps` |
| Stale-stream watchdog (worker silent > 90s) | shell | `service/live/staleStreamSweeper` |

## Tick math

```
debitCents       = ceil(debitSeconds * pricing.liveCentsPerSecond)
runwaySeconds    = floor(remainingBalanceCents / pricing.liveCentsPerSecond)
graceTriggered   = remainingBalanceCents <= 0
```

The shell's session-tick handler atomically decrements `app.balances.balance_cents`
by `debitCents`, clamps at 0 (so a race never creates negative balance),
and returns the post-decrement runway to the worker. Tick sequence is
idempotent: a replayed `seq` returns the current state without
double-debiting.

## Grace semantics

Grace starts when a tick reports `graceTriggered=true` AND a topup attempt
failed. The worker holds the encoder running for `stream_grace_seconds`
(default 60s); if balance recovers above runway threshold within grace,
the deadline clears. If grace expires, the worker calls
`/internal/live/session-ended` with `reason: 'insufficient_balance'`,
the encoder is killed, and the stream transitions to `errored`.

## Topup decoupling

Per the implementation decision in plan 0006, the *gRPC* `TopUpStreamingSession`
call lands in the worker (which holds the gRPC connection to its local
payment-daemon receiver); the *authorization* of how much to top up is a
shell concern (it knows the project balance). The split: shell answers
"is there enough fiat balance for N more seconds?", worker translates a
yes into a `TopUpStreamingSession` against its co-located receiver. End-
to-end shell-mediated TopUp is tech-debt.

## Cross-references

- [`live-state-machine.md`](live-state-machine.md) — where in the lifecycle each step happens.
- [`internal-callback-api.md`](internal-callback-api.md) — wire format for the worker → shell side of this pattern.
- [`payment-integration.md`](payment-integration.md) — VOD reserve→commit (the other half of the engine's payment surface).
- `livepeer-modules/payment-daemon/` — the daemon that implements the underlying primitive.
