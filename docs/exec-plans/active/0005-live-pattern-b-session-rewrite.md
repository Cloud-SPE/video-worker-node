---
id: 0005
slug: live-pattern-b-session-rewrite
title: Live Pattern B session rewrite
status: drafted
owner: agent
opened: 2026-05-02
depends-on: 0003
---

## Goal
Replace the current video live-session contract and runtime flow with the
Pattern B streaming architecture already pinned at the suite level and
validated against the VTuber reference shape. After this plan, live mode
in `video-worker-node` should expose canonical session open/topup/end
routes, own runtime continuation from local receiver-side balance, emit
authoritative worker-to-gateway HTTP events, and separate runtime end
from recording finalization.

## Non-goals
- No backwards-compatibility shims for the old `/stream/*` live routes
  or the old gateway-authoritative tick contract.
- No attempt to implement worker failover or mid-session migration in
  this rewrite. The contract should leave room for it later, but the
  implementation remains single-worker ownership + fail-fast.
- No redesign of VOD or ABR unless shared live-path code forces a
  bounded refactor.
- No shell-side billing/reconciliation implementation. That lands in the
  sibling `livepeer-video-gateway` plan.
- No broad `livepeer-video-core` interface redesign in parallel with
  this worker rewrite.

## Cross-repo dependencies
- `livepeer-video-gateway` needs a sibling live rewrite plan that
  consumes the worker contract frozen here.
- `livepeer-video-core` live alignment remains deferred until the worker
  and gateway session contract is stable in code.

## Approach
- [ ] Freeze the worker-side live contract in repo docs:
      - canonical routes:
        - `POST /api/sessions/start`
        - `POST /api/sessions/{gateway_session_id}/topup`
        - `POST /api/sessions/{gateway_session_id}/end`
      - mandatory correlation fields:
        - `gateway_session_id`
        - `worker_session_id`
        - `work_id`
        - `usage_seq`
      - canonical work unit: seconds
      - worker-to-gateway authoritative event transport: HTTP callbacks
- [ ] Replace the old live route surface in the runtime HTTP layer:
      - remove `/stream/start`, `/stream/stop`, `/stream/topup`,
        `/stream/status` as the canonical contract
      - wire the new session routes through one live-session service
        boundary
- [ ] Introduce a worker-owned live session registry that durably or
      semi-durably tracks:
      - `gateway_session_id -> worker_session_id -> work_id`
      - sender address
      - monotonic `usage_seq`
      - low-balance / grace state
      - recording-finalization phase boundary
- [ ] Rework live payment flow around current payee-daemon primitives:
      - `ProcessPayment` on open
      - `ProcessPayment` on topup against the existing `work_id`
      - periodic `DebitBalance`
      - periodic `SufficientBalance`
      - `CloseSession` exactly once on terminal completion
- [ ] Rework the live runtime state machine:
      - worker owns continuation decisions
      - gateway is informed via event delivery, not queried for stop/go
      - low-balance enters grace locally
      - refilled clears grace locally
      - worker failure is fail-fast and structured
- [ ] Define and emit the canonical authoritative worker event set over
      HTTP callbacks:
      - `session.ready`
      - `session.usage.tick`
      - `session.balance.low`
      - `session.balance.refilled`
      - `session.error`
      - `session.ended`
      - separate post-session recording/finalization event
- [ ] Keep recording finalization as a distinct post-runtime phase:
      - `session.ended` means live runtime is over
      - recording-ready/finalization is emitted separately after runtime
        teardown
- [ ] Rebase live failure handling from plan 0004 onto the new session
      model:
      - preserve fail-fast cleanup intent
      - map worker/runtime/payment failures onto the new terminal
        reasons and event taxonomy
- [ ] Update tests to cover:
      - open/topup/end route lifecycle
      - local debit/runway loop
      - monotonic `usage_seq`
      - low-balance/refilled transitions
      - event delivery shape and idempotency inputs
      - cleanup on graceful and fatal termination
- [ ] Rewrite live design/product docs so they teach the new contract
      instead of the gateway-ledger tick model.

## Decisions log

### 2026-05-02 — Keep public video API Mux-like, but align the internal worker contract to VTuber
Reason: the migration pressure is on the customer-facing video gateway
surface, not on the internal worker routes. The worker contract should
follow the VTuber Pattern B shape (`/api/sessions/start|topup|end`)
while the gateway preserves the product-facing live-stream resource
model.

### 2026-05-02 — Worker-to-gateway authoritative events use HTTP callbacks
Reason: VTuber uses WebSocket because the workload is inherently
interactive. Video live billing/reconciliation does not need a
long-lived interactive control socket for authoritative accounting
events, and HTTP callbacks give simpler idempotent delivery semantics.

### 2026-05-02 — Canonical live work unit is seconds
Reason: debit cadence, runway thresholds, grace windows, and customer
funding all naturally reason in seconds. Customer-facing pricing can
still be presented in minutes, but the worker/gateway contract should
remain second-granular.

### 2026-05-02 — First cut is single-worker ownership, shaped for future failover
Reason: seamless worker failover is desirable but materially broader
than this rewrite because it requires ingest continuity, encoder/HLS
continuity, and explicit payment/session transfer semantics. The
contract should leave room for it later without pretending migration is
implemented now.

### 2026-05-02 — Recording finalization remains a distinct phase from live runtime
Reason: unlike VTuber, video has a real post-runtime recording bridge
and asset finalization concern. Overloading runtime end with
recording-ready would conflate worker teardown with media product state.

## Open questions
- Whether the worker session registry needs durable restart survival in
  first cut, or whether in-memory + fail-fast restart semantics are
  sufficient.
- Exact shape of the single gateway ingest endpoint for worker events in
  the sibling `livepeer-video-gateway` plan.
- Whether the post-session recording event should be named to preserve
  continuity with existing `recording-finalized` terminology or be
  folded into a new canonical `session.recording.ready` style.

## Artifacts produced
- Repo-local worker rewrite plan only. Follow-on PRs should link here.
