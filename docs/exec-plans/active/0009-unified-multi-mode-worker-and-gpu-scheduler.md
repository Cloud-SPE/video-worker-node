---
id: 0009
slug: unified-multi-mode-worker-and-gpu-scheduler
title: Unified multi-mode worker and GPU scheduler
status: active
owner: agent
opened: 2026-05-03
---

## Goal
Redesign `livepeer-video-worker-node` as a clean-break, greenfield
deployment target: one worker process per host, one co-located payment
daemon, all workload modes enabled in the same process, and a shared GPU
scheduler that can run multiple concurrent video workloads safely and
efficiently across NVIDIA, Intel, and AMD hosts. The result should be a
production-grade worker architecture that uses available GPU capacity
instead of hard-serializing work, while remaining explicit about the
payment-daemon contract gaps that require external payment-team signoff
before the full system can be called complete.

## Non-goals
- No backward compatibility for the current single-mode deployment
  model. This plan assumes new deployments only.
- No changes in `livepeer-modules-project` / `payment-daemon` from this
  repo. Required payment work is captured as an external handoff.
- No addition of new ingest protocols such as SRT or WHIP.
- No attempt to solve cross-host scheduling, distributed queueing, or
  multi-worker failover in the first cut. Scope is one worker host using
  one local GPU runtime budget.
- No promise that every vendor path supports identical live-mode
  concurrency on day one; vendor parity is a validation matrix item, not
  an assumption.

## Cross-repo dependencies
- Payment-daemon team signoff is required for the external contract
  changes documented in this plan's payment handoff phase.
- Any consuming shell/gateway that currently assumes worker
  single-modality will need to consume the new unified worker behavior,
  but this plan treats that as downstream integration work rather than a
  local blocker for the refactor.

## Phases

### Phase 1 — Freeze the new worker runtime contract

**Deliverables**
- A repo-local design doc update that replaces the "exactly one mode per
  process" assumption with the canonical new runtime shape:
  - one process exposes VOD, ABR, and live routes together
  - one RTMP ingest surface
  - one shared GPU scheduler
  - one canonical `/registry/offerings` projection derived from the full
    capability catalog
- A clear admission-control model:
  - batch workloads may queue
  - live workloads have reserved headroom and defined overload behavior
  - scheduler decisions are vendor-neutral and operate on detected GPU
    profile plus operator overrides
- A clear statement of payment-daemon contract gaps that are outside
  this repo's write scope.

**Acceptance criteria**
- The docs no longer describe single-mode worker runtime as the target
  deployment model.
- The worker architecture teaches one host-level scheduler across all
  enabled workload types.
- The core product decisions in this plan's decisions log are explicit
  enough to let implementation start without preserving the old
  single-mode architecture.

### Phase 2 — Refactor boot, lifecycle, and HTTP for unified mode

**Deliverables**
- Replace exclusive `--mode=` boot wiring with a unified multi-mode boot
  path.
- Update runtime lifecycle wiring so VOD, ABR, and live runners can all
  be started in one process.
- Update the HTTP layer so mode-specific `wrong_mode` rejections are
  removed and each route is governed by actual runner availability /
  scheduler admission instead.
- Update `/health` and `/registry/offerings` to describe the unified
  worker truthfully.

**Acceptance criteria**
- One daemon instance can accept VOD, ABR, and live requests without
  `501 wrong_mode` behavior.
- Startup no longer branches around a single selected mode.
- Route tests cover the unified-process path for all three workload
  surfaces.

### Phase 3 — Introduce a shared GPU scheduler

**Deliverables**
- A scheduler/provider under `internal/providers/` that owns GPU
  admission for all encode workloads.
- Vendor-neutral scheduling API with vendor-specific capacity profiles.
- Scheduler accounting based on a common resource model, likely:
  - encode-session slots
  - optional live-reserved slots/headroom
  - VRAM budget or weighted preset cost as a safety bound
- Operator-visible metrics for:
  - active slots
  - queued batch jobs
  - live admission failures
  - scheduler wait time

**Acceptance criteria**
- VOD, ABR, and live all acquire/release scheduler capacity through one
  shared boundary.
- The scheduler can enforce bounded concurrency above the current
  single-job execution model.
- Metrics and logs make scheduler decisions debuggable under load.

### Phase 4 — Raise throughput safely

**Deliverables**
- Convert VOD from the current serial loop into a bounded worker pool.
- Allow multiple ABR parent jobs to run concurrently while keeping
  per-rendition sequencing within one ABR job initially.
- Keep live sessions under the same scheduler, with explicit policy for
  reserved headroom and overload behavior.
- Ensure queue limits, cancellation, and teardown semantics remain
  coherent under concurrent load.

**Acceptance criteria**
- One worker process can run more than one video workload at a time on
  hardware that supports it.
- Queueing and overload behavior are explicit and tested.
- Concurrency is bounded by scheduler policy rather than accidental
  goroutine fanout.

### Phase 5 — Payment-team handoff and external signoff

**Deliverables**
- A concrete handoff memo for the payment-daemon team covering:
  - required debit idempotency contract (`debit_seq` or equivalent)
  - required authoritative per-`work_id` session-binding and pricing
    semantics for multi-offering workers
  - required close-session semantics and cleanup behavior
  - expected worker-side concurrency and retry behavior
  - expected compatibility with a unified worker catalog serving many
    simultaneous `(sender, work_id)` sessions
- A verification matrix the payment team can use to sign off:
  - concurrent VOD sessions
  - concurrent ABR sessions
  - concurrent live sessions
  - mixed-mode concurrency against one payee daemon catalog

**Acceptance criteria**
- Worker-side assumptions about the payment daemon are written down in a
  form the payment team can approve or reject precisely.
- This repo does not silently depend on payment semantics that only
  exist in comments or tribal knowledge.
- Final production readiness remains blocked until the payment team
  signs off on the external contract.

### Phase 6 — Production validation and operations

**Deliverables**
- Benchmarks and soak tests for mixed-mode throughput.
- Updated deployment docs for the new one-worker-per-host model.
- Capacity-tuning guidance for operators, including per-vendor override
  knobs where auto-detection is insufficient.
- A residual-risk section covering vendor-specific live validation,
  scheduler calibration drift, and payment-team outstanding items.

**Acceptance criteria**
- The new worker model has a documented production deployment shape.
- Capacity claims are tied to measured validation, not only code-path
  reasoning.
- Remaining vendor-specific limitations are documented as explicit debt
  or follow-on plans.

## Decisions log

### 2026-05-03 — No backward-compatibility path for single-mode worker
Reason: the user explicitly does not want a legacy migration path. The
new deployment model should be simpler and more coherent if the codebase
stops preserving the old "one mode per process" assumption.

### 2026-05-03 — Scheduler must be multi-vendor, not NVIDIA-only
Reason: concurrency is a worker-capacity problem, not a single-vendor
problem. NVIDIA, Intel, and AMD execution paths may differ, but the
scheduling boundary should remain common and vendor-neutral.

### 2026-05-03 — Payment-daemon work is an external signoff gate, not a local implementation task
Reason: this repo cannot modify `livepeer-modules-project`. The plan
must therefore separate worker-only refactor work from the payment
contract changes that another team must implement and verify.

### 2026-05-03 — Authoritative session binding must be explicit in the payee contract
Reason: payment-team review identified that "authoritative per-work_id
pricing" is underspecified unless the payee contract also defines where
that binding enters the system. The worker therefore needs either an
additive `ProcessPayment` / session-open contract carrying pricing
metadata or a payment-team alternative with equivalent authority.

### 2026-05-03 — Session open should create pending work before sender is known
Reason: payment-team follow-up clarified that the cleanest payee
contract is not "OpenSession requires sender". Instead,
`OpenSession(work_id, capability, offering, price_per_work_unit_wei,
work_unit)` should create a pending session, and the first successful
`ProcessPayment(payment_bytes, work_id)` should validate payment,
extract sender, and transition the session to open. Identity after that
transition is `(sender, work_id)`.

### 2026-05-03 — All three vendors are first-class in v1
Reason: the user explicitly does not want a scheduler or deployment
model that is architecturally biased toward NVIDIA. Initial production
validation must therefore include NVIDIA, Intel, and AMD as first-class
vendor families, even if per-vendor tuning differs.

### 2026-05-03 — Live overload fails fast; batch work may queue
Reason: live ingest should not wait behind batch work once reserved
headroom is exhausted. Fail-fast behavior is simpler, safer, and easier
for upstream systems to reason about than bounded waiting or preemption
in the first production cut.

### 2026-05-03 — Scheduler starts slot-based but must be shaped for richer costing
Reason: forcing full VRAM modeling into the first refactor would slow
delivery, but a production-grade design cannot dead-end on naive slot
counts forever. The API should therefore launch with slot-based
admission while leaving a clean path for VRAM / preset-cost budgeting.

### 2026-05-03 — Per-host success target is explicit and vendor-neutral
Reason: "highly efficient" is otherwise untestable. The baseline target
for initial validation is:
- VOD: 4 concurrent jobs per host
- ABR: 2 concurrent parent jobs per host
- Live: 4 concurrent live sessions per host
- Mixed: 2 VOD + 1 ABR + 2 live on one host
- Sustained GPU utilization target: 70%–90% under steady-state load

### 2026-05-03 — Auto-detect remains the default, with operator overrides
Reason: production systems need good defaults, but heterogeneous GPUs
including older consumer cards and vendor-specific driver behavior make
manual tuning necessary. The worker should therefore auto-detect
capacity and expose override knobs per host.

## Progress log

### 2026-05-03 — Plan drafted
- Captured the clean-break deployment direction: one unified worker
  process, one payment daemon, one host-level scheduler.
- Captured the external payment-team contract/signoff requirement as an
  explicit phase instead of burying it in open questions.

### 2026-05-03 — Open product decisions resolved with operator steer
- Resolved vendor scope as NVIDIA + Intel + AMD first-class in v1.
- Resolved live overload policy as fail-fast.
- Resolved scheduler v1 shape as slot-based with a forward path for
  VRAM / preset-cost budgeting.
- Resolved baseline success target for concurrent VOD / ABR / live.
- Resolved capacity tuning approach as auto-detect plus operator
  overrides.

### 2026-05-03 — Payment-team review accepted the direction but widened the external scope
- Payment team approved debit idempotency in principle, with
  `(sender, work_id, debit identifier)` idempotency semantics.
- Payment team approved `CloseSession` direction in principle.
- Payment team requested that session-pricing semantics be restated as
  an explicit session-binding contract, not only "storage semantics".
- Payment team did not sign off on unified-catalog concurrency yet
  because restart safety and same-session concurrency atomicity are not
  verified in the current payee implementation.
- Payment team indicated this requires a real cross-module exec-plan in
  `livepeer-modules-project`, not a small tweak.

### 2026-05-03 — Minimum external signoff bar is now explicit
Reason: payment-team review narrowed the external dependency down to
four concrete requirements: approve `debit_seq` or equivalent debit
idempotency, choose an authoritative session-binding mechanism, define
`CloseSession` as terminal and idempotent, and open a
`livepeer-modules-project` exec-plan covering proto/state/atomicity/
verification work. Worker-side production readiness remains blocked
until those items are accepted and verified.

### 2026-05-03 — Payment team acknowledged scope but did not issue final signoff
Reason: payment team responded that the signoff bar is accepted only as
an acknowledgment of scope. Final signoff still requires their own
exec-plan plus a concrete design response covering:
- approve / reject / needs revision per requested change
- proto/RPC delta for `debit_seq` or equivalent
- chosen authoritative session-binding mechanism
- terminal `CloseSession` semantics
- verification scope/results
- worker-side blockers or constraints

### 2026-05-03 — Phase 1 implementation started with a doc-level contract freeze
Reason: the first concrete step in this repo is to rewrite the top-level
architecture and operations docs so the unified multi-mode worker is the
canonical runtime model. This reduces ambiguity before the runtime
refactor begins.

### 2026-05-03 — Phase 2 wiring refactor started
Reason: boot, lifecycle, and HTTP route gating now begin shifting away
from the old single-mode assumption. The worker defaults to unified
mode, lifecycle no longer keys runner startup solely off one selected
mode, and HTTP route behavior moves from `wrong_mode` rejection toward
runner-availability semantics.

### 2026-05-03 — Phase 3 scheduler boundary landed in first cut
Reason: a provider-owned slot scheduler now exists and is threaded into
VOD, ABR, and live encode admission. The first cut now includes
slot-based scheduling, reserved live headroom, fail-fast live overload,
weighted preset-derived cost admission, and operator override flags for
slot/cost capacity and host-level cost scaling. Richer VRAM-aware
costing remains follow-on work.

### 2026-05-03 — Phase 4 throughput work started with bounded VOD and ABR pools
Reason: the scheduler boundary alone does not increase throughput while
the batch runners remain single-threaded. VOD now runs through a bounded
worker pool keyed off `max_queue_size`, ABR now allows concurrent parent
jobs while preserving sequential per-rendition encoding inside a single
job, and the operator capacity report now exposes active/queued batch
load instead of only queue depth.

### 2026-05-03 — Scheduler state is now visible on health, capacity, and metrics surfaces
Reason: production tuning needs a consistent operator view of slot
pressure. The worker now projects scheduler totals, reserved live
headroom, active slots, and queued batch work through `/health`,
`GetCapacity()`, and the metrics listener, reducing the gap between
runtime admission decisions and operator-visible telemetry.

### 2026-05-03 — Scheduler admission is now weighted by preset-derived cost
Reason: slot-only admission treats unlike workloads as interchangeable.
The scheduler now accepts workload cost units in addition to slots, with
the runners deriving first-cut static costs from preset resolution,
bitrate, codec, and workload type. This keeps slots as the hard ceiling
while reducing over-admission risk for heavier live and high-resolution
transcodes.

### 2026-05-03 — Payment implementation review found remaining correctness blockers
Reason: a quick worker-side review of the in-flight payment-daemon
implementation showed that external signoff is still not ready. The key
issues are: `ProcessPayment` can currently validate/enqueue ticket side
effects before session sender/state enforcement commits, `DebitBalance`
replay handling does not yet reject mismatched `work_units` for a reused
`debit_seq`, and the payee gRPC surface still collapses unknown
session/storage failures into `InvalidArgument`. Worker-side
production-readiness therefore remains blocked on payment fixes, not
only on formal review.

### 2026-05-03 — External payment contract blocker cleared by payment-daemon v4.0.0
Reason: a targeted worker-side recheck of the released
`livepeer-payment-daemon v4.0.0` confirmed that the previously-blocking
payee issues are now fixed: `ProcessPayment` validates the session
target before redemption side effects, `DebitBalance` enforces replay
consistency for `(sender, work_id, debit_seq)`, and unknown payee
failures surface as server-side errors. The worker repo is now updated
to consume `OpenSession` and wire-level `debit_seq` directly.

## Open questions
- Which concrete GPU families should be mandatory in the first hardware
  validation matrix for each vendor, e.g. consumer versus datacenter
  NVIDIA, Intel Arc versus iGPU, AMD consumer versus datacenter parts?
- Whether the first production cut should include only safe
  non-preemptive scheduling, or whether partial batch preemption is
  worth evaluating as a follow-on design doc once the scheduler exists.
- What exact operator override surface should ship first: CLI flags,
  config-file fields, or both.

## Artifacts produced
- This exec-plan only. Follow-on worker refactor PRs should link here.
- [`../../design-docs/payment-daemon-change-request-unified-worker.md`](../../design-docs/payment-daemon-change-request-unified-worker.md)
  — repo-local payment-team handoff memo for external contract work and
  verification.
