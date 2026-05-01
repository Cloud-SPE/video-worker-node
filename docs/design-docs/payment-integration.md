---
title: Payment integration
status: accepted
last-reviewed: 2026-04-26
---

# Payment integration

How the shell pays workers and how the customer's wallet gets charged.

> **Status**: drafted. Worker-side implementations live in
> `internal/service/paymentbroker/` (VOD/ABR debit pipeline) and
> `internal/service/liverunner/` (streaming-session pattern for live).

## Two parallel concerns

There are two distinct payment loops and they shouldn't be confused:

1. **Shell ↔ worker payment**: probabilistic micropayment tickets per
   transcode unit (or per debit-tick for live), routed via
   `livepeer-modules/payment-daemon`. Worker's payment-daemon
   receiver validates; shell's payment-daemon sender mints. Wire
   compatibility with `go-livepeer`'s `net.Payment` is preserved by
   `livepeer-modules`.
2. **Customer ↔ shell wallet**: the project's prepaid balance gets reserved
   and committed via the engine's `Wallet` adapter. The shell's
   `prepaidWallet` impl writes to `app.balances` and `app.reservations`.

These loops are independent. The shell could swap its `Wallet` impl (e.g.,
to a postpaid Stripe-billed model post-MVP) without changing anything about
how it pays workers. Conversely, the worker payment scheme could change
(e.g., a future non-Livepeer payment system) without touching the customer
wallet.

## Bridge-pattern dispatch

For VOD encoding (one discrete request → one discrete payment):

```
Shell (livepeer-video-gateway)
  │
  ├─ Engine dispatcher: dispatchVodSubmit / scheduleEncodeJobs
  │
  ├─ wallet.reserve(callerId, costQuote) → reservation_handle
  │     ↑ (writes to app.reservations + decrements app.balances.reserved_cents)
  │
  ├─ workerResolver.resolveByCapability('video:transcode.vod')
  │     ↑ backed by the coordinator/operator roster populated from worker
  │       GET /registry/offerings scrapes
  │
  ├─ workerClient.callWorker({ workerUrl, path: '/v1/video/transcode', ... })
  │     │
  │     ├─ payment-daemon (sender mode, co-located) creates a ticket for
  │     │   ({ recipient: <worker-eth-addr>, capability, callerId })
  │     │
  │     └─ HTTP POST {workerUrl}/v1/video/transcode
  │           Headers: livepeer-payment: <base64(payment_bytes)>
  │           Body:    { input_url, job_id, rendition: {...} }
  │
  │                              ▼
  │              Worker (livepeer-video-worker-node)
  │                ├─ payment-daemon (receiver mode, co-located) validates
  │                │   ProcessPayment(payment_bytes, work_id)
  │                ├─ DebitBalance(estimated_units)
  │                ├─ FFmpeg subprocess does the work
  │                ├─ DebitBalance(actual_units − estimated_units) (over-debit only)
  │                └─ HTTP 200 { output_storage_keys, actual_seconds, ... }
  │
  ├─ on success: wallet.commit(handle, usageReport)
  │     ↑ (decrements app.balances.balance_cents; settles reservation)
  │
  └─ on failure: wallet.refund(handle)
        ↑ (releases reserved_cents; reservation marked refunded)
```

This is the same shape as `openai-livepeer-bridge` → `openai-worker-node`.
Mature, well-trodden.

## Streaming-session pattern (live)

For live HLS (one open session → many continuous debits):

```
Worker accepts RTMP, calls back to shell to validate stream key
  │
  └─ Shell payment-daemon: OpenStreamingSession({ stream_id, capability,
                                                   initial_credit_seconds: 60 })
                              ↑ session_id
                                ↓
Worker debits every 5s (cadence):
  └─ payment-daemon: DebitStreamingSession({ session_id, seq: <monotonic>,
                                              debit_seconds: <delta> })
                              ↑ remaining_runway_seconds
                                ↓
If runway < 30s, worker → shell /internal/live/topup:
  └─ Shell: payment-daemon.TopUpStreamingSession({ session_id, more_seconds })
                              ↑ ok? insufficient_balance?
                                ↓
If runway hits 0 + grace_seconds (60s) elapses:
  └─ Worker closes FFmpeg, payment-daemon:
       CloseStreamingSession({ session_id, final_seq, reason: 'insufficient_balance' })

On graceful broadcaster disconnect:
  └─ payment-daemon: CloseStreamingSession({ session_id, final_seq, reason: 'broadcaster_disconnect' })
```

Specs:
- The pattern itself is `livepeer-modules/payment-daemon`'s
  contribution; we consume it.
- The live state machine that wraps it lives in
  [`live-state-machine.md`](live-state-machine.md) (lands in plan 0006).
- The shell-side reservation/ledger interaction during a streaming session
  lives in [`wallet-and-billing.md`](wallet-and-billing.md) (lands in plan
  0004 and gets extended in plan 0006).

## Why the engine doesn't know about payment-daemon

The engine has `WorkerClient` as an adapter. The shell's `WorkerClient`
impl wraps `payment-daemon`. From the engine's perspective, "calling a
worker" is a black-box async call; how payment gets attached is an
implementation detail.

This means an external operator who doesn't use Livepeer payment (e.g.,
they're metering against their own internal credits system) can write a
`WorkerClient` impl that doesn't talk to `payment-daemon` at all. The
engine doesn't care.

## Cross-references

- [`adapter-contracts.md`](adapter-contracts.md) §`Wallet`,
  §`WorkerClient`, §`WorkerResolver`
- [`worker-discovery.md`](worker-discovery.md) — service-registry resolution
  detail
- [`livepeer-modules`](https://github.com/Cloud-SPE/livepeer-modules)
  README — daemons we consume
- Plan 0004 §D.7 — `paymentDaemonWorkerClient` impl
- Plan 0006 §C — streaming-session lifecycle in the worker
