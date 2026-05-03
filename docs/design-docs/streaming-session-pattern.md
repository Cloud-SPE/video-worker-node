---
title: Streaming-session payment pattern
status: accepted
last-reviewed: 2026-05-03
---

# Streaming-session payment pattern

How live streams pay over time. This document is in transition from the
older shell-authoritative live loop to the Pattern B worker-owned
runtime model defined in plan 0005. The target shape still follows
`livepeer-modules/payment-daemon`'s streaming-session pattern, with the
responsibilities split between the **worker** (runtime authority), the
**payment-daemon** (receiver tracks credit, sender mints tickets), and
the **gateway/shell** (owns customer USD balance and decides whether to
mint more ETH-denominated payment credit).

## Why this pattern (not VOD-style reserve→commit)

VOD encodes are discrete: the engine reserves up-front, runs the job,
commits the actual usage at the end. Live broadcasts don't fit:

- Total duration is unknown when the stream starts.
- Holding a giant reservation up-front would lock more balance than the
  customer needs.
- A broadcaster shouldn't get cut off the moment their balance dips below
  some threshold — they need a small grace window to top up.

Streaming sessions debit small amounts continuously, with a runway
watermark and a grace period. Under Pattern B the worker stops the
stream cleanly when receiver-side balance can no longer sustain it; the
gateway's job is to keep feeding fresh credit while the customer's USD
balance authorizes it.

## The five-step recipe

1. **Open**: gateway resolves a worker, mints direct payment credit, and calls the worker's canonical `POST /api/sessions/start`. The worker chooses `work_id`, opens a pending payee session with `OpenSession(...)`, then runs the first `ProcessPayment(...)` to seal sender and returns `worker_session_id`.
2. **Tick**: every 5s (`debit_cadence`), the worker calls receiver-side `DebitBalance(sender, work_id, units, seq)` with the encoded-seconds delta since the last tick. `seq` is monotonically increasing per session — replays of the same `seq` are receiver-side no-ops.
3. **Watermark**: after each debit, the worker runs `SufficientBalance(sender, work_id, runway_seconds)` locally. If runway is low, the worker emits `session.balance.low` to the gateway and starts grace locally.
4. **Top-up**: gateway checks whether the customer's USD balance can continue funding the stream. If yes, it mints fresh payment credit and calls the worker's canonical `POST /api/sessions/{gateway_session_id}/topup`; the worker applies it to the existing `work_id` via `ProcessPayment(...)`. If not, customer-facing low-balance semantics are exposed from the gateway side while the worker counts down grace.
5. **Close**: graceful broadcaster disconnect or explicit operator stop ends the runtime session. The worker emits `session.ended`, closes the receiver-side payment session, and later emits a separate recording-ready event if recording finalization succeeds.

## Worker / shell / daemon responsibilities

| Concern | Owner | Where |
|---|---|---|
| Per-session state machine | worker | `apps/transcode-worker-node/internal/service/liverunner` |
| `seq` counter persistence | worker | BoltDB via `repo/jobs.IncrementDebitSeq`; survives worker restart |
| `OpenSession` / `ProcessPayment` / `DebitBalance` / `SufficientBalance` / `CloseSession` | worker | calls into co-located `payment-daemon` (receiver) |
| Project balance lookup + topup authorization | gateway | decides whether to mint additional payment credit |
| Per-second cost basis (`liveCentsPerSecond`) | gateway | pricing/offering logic |
| Customer billing ledger lifecycle | gateway | derived from accepted worker `session.usage.tick` events |
| Session correlation + usage event ingest | gateway | canonical Pattern B event-ingest path |

## Tick math

The canonical unit is stream seconds. The worker debits whole-second
units locally and emits `session.usage.tick` with the accepted debit:

```
units             = ceil(encodedSecondsDelta)
runwayThreshold   = 30 seconds
graceWindow       = 60 seconds
```

Customer-facing billing may still be presented in minutes, but the
worker/gateway contract stays second-based for tick precision, runway
checks, and idempotent replay handling.

## Grace semantics

Grace starts when the worker detects insufficient receiver-side runway.
The worker holds the encoder running for `stream_grace_seconds`
(default 60s); if fresh credit lands before grace expires, the deadline
clears and the worker emits `session.balance.refilled`. If grace
expires, the worker ends the runtime session with
`reason: 'insufficient_balance'`.

## Topup decoupling

The topup split is now explicit:

- gateway answers "can this customer's USD balance continue funding the stream?"
- gateway mints direct payment credit when the answer is yes
- worker applies that payment to the existing `work_id`

The worker should never expose ETH/ticket depletion directly to
customers. Gateway-facing low-balance is an internal operational signal;
customer-facing low-balance is derived from USD-side funding state.

## Cross-references

- [`live-state-machine.md`](live-state-machine.md) — legacy shell-side lifecycle; pending rewrite to Pattern B event semantics.
- [`internal-callback-api.md`](internal-callback-api.md) — legacy callback taxonomy retained only while the gateway rewrite is in flight.
- [`payment-integration.md`](payment-integration.md) — VOD reserve→commit and live payment split.
- `livepeer-modules/payment-daemon/` — the daemon that implements the underlying primitive.
