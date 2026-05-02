---
title: Payment integration
status: accepted
last-reviewed: 2026-04-26
---

# Payment integration

How the gateway pays workers and how the customer's wallet gets charged.

> **Status**: drafted. Worker-side implementations live in
> `internal/service/paymentbroker/` (VOD/ABR debit pipeline) and
> `internal/service/liverunner/` (streaming-session pattern for live).

## Two parallel concerns

There are two distinct payment loops and they shouldn't be confused:

1. **Gateway ↔ worker payment**: probabilistic micropayment tickets per
   transcode unit (or per debit-tick for live), routed via
   `livepeer-modules/payment-daemon`. Worker's payment-daemon
   receiver validates; gateway's payment-daemon sender mints. Wire
   compatibility with `go-livepeer`'s `net.Payment` is preserved by
   `livepeer-modules`.
2. **Customer ↔ gateway wallet**: the project's prepaid balance gets reserved
   and committed via the engine's `Wallet` adapter. The shell's
   `prepaidWallet` impl writes to `app.balances` and `app.reservations`.

These loops are independent. The gateway could swap its `Wallet` impl (e.g.,
to a postpaid Stripe-billed model post-MVP) without changing anything about
how it pays workers. Conversely, the worker payment scheme could change
(e.g., a future non-Livepeer payment system) without touching the customer
wallet.

## Bridge-pattern dispatch

For VOD encoding (one discrete request → one discrete payment):

```
Gateway (livepeer-video-gateway)
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

## Streaming-session pattern (live, Pattern B target)

For live HLS (one open session → many continuous debits):

```
Gateway resolves worker, mints initial payment credit, opens worker session
  │
  └─ Worker: ProcessPayment(payment_bytes, work_id)
                              ↑ worker_session_id + work_id
                                ↓
Broadcaster connects RTMP; worker accepts and starts encoder
  │
Worker debits every 5s (cadence):
  └─ payment-daemon: DebitBalance({ sender, work_id, seq: <monotonic>, units: <seconds> })
                                ↓
Worker checks local runway:
  └─ payment-daemon: SufficientBalance({ sender, work_id, min_units: 30 })
                                ↓
Worker emits usage/accounting events to gateway:
  └─ POST /internal/live/events
       types: session.ready, session.usage.tick, session.balance.low,
              session.balance.refilled, session.ended, session.recording.ready
                                ↓
If worker signals low balance:
  └─ Gateway decides whether customer USD balance authorizes more funding
       ├─ yes: mint fresh payment and POST /api/sessions/{gateway_session_id}/topup
       └─ no: expose customer-visible low balance and let worker's grace timer run
                                ↓
If grace expires:
  └─ Worker closes FFmpeg, emits session.ended(reason=insufficient_balance),
     and closes the receiver-side payment session
```

Specs:
- The pattern itself is `livepeer-modules/payment-daemon`'s
  contribution; we consume it.
- The worker-side live session pattern is being rewritten under
  [`streaming-session-pattern.md`](streaming-session-pattern.md) and plan 0005.
- The gateway-side billing/event-ingest rewrite is the companion effort in
  the sibling gateway repo.

## Customer-visible balance semantics

The customer should only see USD-side funding state. They should not see:

- receiver-side ticket runway
- `work_id`
- orch-side ETH depletion
- internal low-balance events from the worker

The worker's `session.balance.low` event is an internal signal telling
the gateway it may need to mint more payment credit. The gateway turns
that into customer-visible low-balance only when the customer's USD
balance can no longer authorize continued funding.

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
