---
title: Active exec-plans
status: accepted
last-reviewed: 2026-05-01
---

# Active exec-plans

In-flight work. Each plan is a contract between humans (who steer) and agents (who execute).

Plan format and lifecycle: [`PLANS.md`](../../../PLANS.md).

## In flight

- [`0004-live-mode-session-failure-contract.md`](0004-live-mode-session-failure-contract.md) — fail-fast live encode error surfacing and resource cleanup for the v3.0.1 live-session contract. Depends on 0003. Status: **in progress**.
- [`0005-live-pattern-b-session-rewrite.md`](0005-live-pattern-b-session-rewrite.md) — drafted replacement plan for the video live session architecture rewrite: VTuber-aligned internal session routes, worker-owned Pattern B debit loop, authoritative worker-to-gateway HTTP events, and separation of runtime end from recording finalization. Depends on 0003. Status: **drafted**.
- [`0009-unified-multi-mode-worker-and-gpu-scheduler.md`](0009-unified-multi-mode-worker-and-gpu-scheduler.md) — clean-break worker redesign: one multi-mode worker process per host, shared multi-vendor GPU scheduler, concurrent VOD/ABR/live execution, and an explicit payment-team handoff/signoff phase for external contract gaps. Status: **active**.
