---
id: 0005
slug: payment-daemon-v3-contract-alignment
title: Align worker payment-daemon and gateway contract to the v3 flow
status: completed
owner: agent
opened: 2026-05-02
depends-on: 0003
---

## Goal
Bring `video-worker-node` into line with the payment and worker-call
contract already assumed by the current `livepeer-video-core` and
`livepeer-video-gateway` repos: paid HTTP routes accept the
`livepeer-payment` header, live control routes tolerate the shell's
`stream_id` naming, worker deploy artifacts reflect the current
payment-daemon image/socket conventions, and the worker exposes the
payee-side `POST /v1/payment/ticket-params` helper needed for the v3
ticket-param flow.

## Non-goals
- Reworking sender-side ticket sizing or resolver selection in the
  shell repos.
- Replacing the worker's payment model or removing payment-daemon
  startup verification outright.
- Adding new paid worker routes unrelated to existing VOD / ABR / live
  control flows. The `ticket-params` helper is unpaid and scoped to the
  existing daemon contract.

## Cross-repo dependencies
- `livepeer-video-core` already defines the worker adapter contract as
  `livepeer-payment` header transport plus selected-route metadata.
- `livepeer-video-gateway` already computes `face_value` gateway-side
  and sends worker calls using the header contract.
- `openai-worker-node` already exposes a worker-authenticated
  `POST /v1/payment/ticket-params` helper over the same payee daemon;
  this repo should converge on that surface.

## Approach
- [x] Add worker-side payment extraction helpers so VOD / ABR accept the
      `livepeer-payment` header as canonical input, while keeping
      request-body `payment_ticket` compatibility where needed during
      rollout.
- [x] Make live worker control routes accept the shell's `stream_id`
      naming without breaking existing `work_id` callers.
- [x] Revisit startup daemon-catalog verification so capability drift
      remains fail-closed while payment-pricing ownership changes do not
      force needless boot failures.
- [x] Align compose files, example config, and operator docs with the
      current payment-daemon image/socket convention used by sibling
      repos.
- [x] Add an authenticated worker-side `POST /v1/payment/ticket-params`
      route that proxies `GetTicketParams` to the local receiver daemon
      without changing the existing payment-processing flow.
- [x] Add focused tests covering header-vs-body payment intake, live
      identifier compatibility, ticket-params proxying, and any changed
      startup verification behavior.

## Decisions log

### 2026-05-02 — Worker is the primary repo to change first
Reason: the gateway and engine already encode the current contract in
their worker adapter surfaces. The worker is the lagging side of the
integration, so changes should land here first and only spill into
other repos if concrete incompatibilities remain after the worker is
aligned.

### 2026-05-02 — Ticket-params helper is part of worker parity, not a follow-up
Reason: both OpenAI and video workers share the same receiver-side
payment-daemon contract. Cost sizing still comes from offerings /
resolver selection, but the payee-owned cryptographic ticket params are
a separate lookup the worker should expose consistently.

## Open questions
- None at completion. Startup validation now remains strict on
  capability / work-unit / offering identity while exact daemon price
  echoing no longer blocks boot. The gateway conclusion is that it
  should keep using payer-daemon `CreatePayment` directly rather than
  calling the worker helper.

## Artifacts produced
- `internal/runtime/http/http.go`
- `cmd/livepeer-video-worker-node/run.go`
- `cmd/livepeer-video-worker-node/verify.go`
- `compose.yaml`
- `compose.prod.yaml`
- `worker.example.yaml`
- `docs/product-specs/http-api.md`
- `docs/operations/running-the-daemon.md`
- `internal/providers/paymentclient/paymentclient.go`
- related tests

### 2026-05-02 — Gateway should not call worker ticket-params directly
Reason: the gateway's payer-daemon `CreatePayment` path already
encapsulates the payee-side ticket-params lookup. The worker helper is
part of the worker/payee contract surface, not a new public gateway
entry point.
