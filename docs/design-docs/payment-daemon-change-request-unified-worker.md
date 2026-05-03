---
title: Payment-daemon change request for unified multi-mode worker
status: accepted
last-reviewed: 2026-05-03
---

# Payment-daemon change request for unified multi-mode worker

This document is the worker repo's formal handoff record for the
payment-daemon work required by plan
[`../exec-plans/active/0009-unified-multi-mode-worker-and-gpu-scheduler.md`](../exec-plans/active/0009-unified-multi-mode-worker-and-gpu-scheduler.md).

`livepeer-payment-daemon v4.0.0` implemented the released payee session
contract this request asked for: `OpenSession`, sender sealing on first
`ProcessPayment`, wire-level `debit_seq`, and terminal `CloseSession`.
This document is retained for traceability and rollout context.

The worker is being redesigned as a unified multi-mode process:

- one worker process per host
- VOD + ABR + live enabled together
- one shared GPU scheduler across all workload types
- multiple concurrent pending/open sessions on the same host, with
  identity sealing to `(sender, work_id)` after first successful
  payment
- one co-located payment daemon serving the worker's full catalog

This repo cannot implement the payment-daemon changes directly. The
payment-daemon team must review, sign off on, and implement the
contract work described below before the full worker architecture can be
called production-ready.

## Requested target architecture

The worker team is explicitly targeting this deployment shape:

- one worker process per host
- one co-located payment daemon per host
- one unified capability catalog per host

The desired end state is **not** one payment daemon per worker mode.
Separate per-mode payment daemons are an implementation workaround for
the current worker design, not the target architecture this request is
asking the payment team to support.

## Summary of requested changes

1. Add explicit debit idempotency to the payee contract.
2. Add authoritative session-binding semantics so a `work_id` is bound
   to capability, offering, price-per-work-unit, and work-unit kind.
3. Implement real `CloseSession` semantics and cleanup.
4. Verify one payee daemon can safely serve a unified worker catalog
   under concurrent VOD, ABR, and live load.

## Why this change is needed

Today the worker can already create multiple payment sessions because
balances are keyed by `(sender, work_id)`. However, the current payment
contract still leaves gaps that are acceptable for a single-mode MVP
but too weak for a production unified worker:

- `DebitBalance` does not carry a debit sequence on the wire, so
  idempotent retries are not explicit.
- Worker-side comments already assume monotonic debit sequence semantics
  even though the v1 wire payload cannot express them.
- `DebitBalance` currently interprets `work_units` directly as wei in
  the receiver implementation, which is insufficient for a worker
  serving multiple capabilities and offerings with different prices.
- `CloseSession` is currently a no-op skeleton, so residual session
  cleanup is underspecified.
- Current balance mutation semantics are not strong enough for
  concurrent same-session debit/topup behavior without an atomic session
  state/update design.

## Requested contract changes

### 1. Debit idempotency

The worker needs an explicit idempotent debit contract for retries.

Requested behavior:

- Preferred shape: extend the payee-side debit request with
  `debit_seq`.
- Acceptable alternative: an equivalent monotonic debit identifier or
  idempotency mechanism proposed by the payment team, provided it gives
  the worker the same correctness properties.
- Idempotency key scope should be at least:
  - sender
  - work_id
  - debit identifier
- Replaying the same debit identifier must be a no-op that returns the
  same resulting balance or another unambiguous idempotent success
  signal.
- A new debit with a new sequence must be applied exactly once.

Reason:

- Live mode issues periodic debits.
- Unified worker mode will run many sessions concurrently.
- Network retries and worker restarts must not double-debit.

### 2. Authoritative session binding and pricing semantics

The worker needs the payee daemon to understand session pricing as more
than "subtract this many wei". This is a semantic requirement, not a
storage-prescription request.

Requested behavior:

- The payee daemon must be able to answer authoritatively what pricing
  and session context a `work_id` represents.
- The session-open contract must support a pending session state:
  - `OpenSession(work_id, capability, offering, price_per_work_unit_wei, work_unit)`
    creates a pending session without requiring sender at open time.
  - The first successful `ProcessPayment(payment_bytes, work_id)`
    validates payment, extracts sender, and transitions the session to
    open.
  - Session identity after that transition is `(sender, work_id)`.
- The pricing/session semantics must support sessions opened against
  different capabilities and offerings from one unified worker catalog.
- At minimum, the payment team should define how a `work_id` binds to:
  - capability
  - offering
  - price per work unit
  - work unit kind
- The binding mechanism itself must be explicit. The worker team is
  explicitly requesting either:
  - an additive `ProcessPayment` / session-open contract that carries
    the authoritative session pricing metadata, or
  - a payment-team alternative that gives the same authoritative
    per-`work_id` binding.
- The payment team may choose the persistence/storage implementation,
  but the semantics above are required.

Reason:

- A unified worker serves multiple offerings concurrently.
- VOD, ABR, and live may all run against different pricing rows.
- Long-term correctness should not rely on every worker caller
  pre-multiplying units into wei with no server-side session context.
- The worker team's original request was underspecified unless the
  payee-side contract explicitly states where the daemon learns this
  binding.

### 3. Close-session semantics

The worker needs a real end-of-session contract.

Requested behavior:

- `CloseSession` must stop being a no-op.
- `CloseSession` must be idempotent.
- Closed sessions must reject future debit calls.
- Closed sessions must reject future topup calls.
- Residual balance may remain queryable for audit/debug for a payment-
  team-defined retention window.
- Cleanup / GC timing may be implementation-defined, but caller-visible
  semantics must be explicit and documented.

Reason:

- Unified worker hosts will generate many short-lived VOD/ABR sessions
  plus long-lived live sessions.
- Production operation needs deterministic cleanup semantics.

### 4. Unified-catalog compatibility

The payment daemon must verify that one catalog can safely back one
unified worker process under concurrency.

Requested behavior:

- Confirm that `ListCapabilities`, `ProcessPayment`, `DebitBalance`,
  `SufficientBalance`, and `CloseSession` all remain correct when one
  worker serves:
  - `video:transcode.vod`
  - `video:transcode.abr`
  - `video:live.rtmp`
- Confirm that the daemon can handle many simultaneous sessions against
  many offerings from the same catalog without catalog/session drift or
  accounting ambiguity.
- Confirm that this works in the target architecture of one worker +
  one payment daemon + one unified catalog per host.

## Assumptions the worker plans to make

The worker refactor in this repo is planning around these assumptions.
The payment team should either approve them or propose replacements.

1. Multiple concurrent sessions against one payee daemon are valid and
   supported.
2. `work_id` can exist in a pending pre-payment state, and
   `(sender, work_id)` becomes the identity boundary once the first
   `ProcessPayment` seals sender.
3. Debit retries need explicit idempotency at the payee contract layer.
4. One unified capability catalog can serve many offerings and many
   sessions safely.
5. Session close has deterministic semantics and can be retried.

## Division of ownership

The worker team is explicitly dividing responsibility this way:

- Payment team owns:
  - debit idempotency correctness
  - authoritative session-binding and pricing semantics
  - `CloseSession` semantics
  - payee behavior under concurrent sessions
- Worker team owns:
  - unified multi-mode runtime
  - shared GPU scheduler
  - queueing / overload behavior
  - route and lifecycle behavior
- End-to-end mixed-workload validation is joint signoff.

## Proposed verification matrix

The worker team requests signoff against at least the following matrix.

### Concurrency correctness

- 4 concurrent VOD sessions against distinct `work_id`s
- 2 concurrent ABR parent sessions against distinct `work_id`s
- 4 concurrent live sessions with periodic debit activity
- Mixed load:
  - 2 VOD
  - 1 ABR
  - 2 live

### Retry and idempotency

- Retry the same debit for one live session and verify no double charge
- Retry a close call and verify deterministic success
- Retry a topup / additional payment for an existing live `work_id`
- Verify that first `ProcessPayment(payment_bytes, work_id)` seals
  sender and transitions the session from pending to open exactly once

### Catalog correctness

- One payee daemon catalog containing VOD, ABR, and live offerings
- Sessions opened concurrently against multiple offerings in the same
  catalog
- Validation that pricing/session metadata does not drift across
  offerings

### Failure behavior

- Worker-side timeout during debit call followed by retry
- Worker-side timeout during close followed by retry
- Daemon restart while multiple session balances exist
- Concurrent debit and topup against the same `(sender, work_id)`
  without lost-update behavior

## Requested response from payment team

The worker team needs a response in this shape:

1. Approved / rejected / needs revision for each requested change
2. Proposed proto or RPC delta for debit idempotency
3. Proposed authoritative per-`work_id` session-binding semantics, and
   where that binding enters the payee contract
4. Proposed `CloseSession` semantics
5. Verification results or planned verification scope
6. Any blocking concerns the worker plan must account for

## Minimum signoff bar

The worker team and payment-team review are aligned that the minimum
payment-team signoff bar is:

1. Approve `debit_seq` or an equivalent idempotent debit semantic.
2. Choose and document one authoritative per-session binding mechanism.
3. Define `CloseSession` as terminal and idempotent.
4. Open a payment-daemon exec-plan in `livepeer-modules-project`
   covering proto, state, atomicity, and verification work.

## Current external state

The payment-team contract request has now been satisfied by
`livepeer-payment-daemon v4.0.0`, and the worker repo has integrated
that released surface.

Worker-side verification of the released daemon confirmed that the
previous blockers are cleared:

1. `ProcessPayment` now validates the session target before ticket
   acceptance/redemption side effects.
2. `DebitBalance` replay handling now rejects conflicting reuse of the
   same `(sender, work_id, debit_seq)` with different `work_units`.
3. Unknown payee/session failures now surface as server-side errors
   rather than caller blame.

This document therefore moves from "active handoff" to "accepted
historical record". Remaining readiness work is in the worker repo's own
runtime, calibration, and deployment path rather than the external
payment contract.

## Required follow-up from payment team

Final payment-team signoff is pending a concrete design response
covering:

1. approved / rejected / needs revision for each requested change
2. proposed proto/RPC delta for `debit_seq` or equivalent
3. chosen authoritative session-binding mechanism
4. terminal `CloseSession` semantics
5. verification scope/results
6. any worker-side blockers or constraints

## Acceptance criteria for payment-team signoff

This signoff bar was satisfied once the payment team either:

- accepts the requested semantics as written, or
- proposes technically equivalent replacements that the worker team
  explicitly approves

for all of the following:

- debit idempotency
- authoritative per-`work_id` session-binding and pricing semantics
- non-noop `CloseSession`
- unified-catalog correctness under concurrent VOD, ABR, and live load

## Worker-side status until signoff

The external payment contract work has landed. Worker-side production
readiness now depends on the worker runtime, operator defaults, and
hardware validation captured in plan `0009`, not on further payment
contract negotiation.
