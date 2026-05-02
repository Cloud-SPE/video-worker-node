---
title: Active exec-plans
status: accepted
last-reviewed: 2026-05-01
---

# Active exec-plans

In-flight work. Each plan is a contract between humans (who steer) and agents (who execute).

Plan format and lifecycle: [`PLANS.md`](../../../PLANS.md).

## In flight

- [`0003-v3-0-1-worker-contract-alignment.md`](0003-v3-0-1-worker-contract-alignment.md) — shared `worker.yaml`, canonical `/registry/offerings`, `/health`, archetype-A deploy/docs sweep, and proto refresh for the finalized v3.0.1 worker contract. Depends on 0001. Status: **in progress**.
- [`0004-live-mode-session-failure-contract.md`](0004-live-mode-session-failure-contract.md) — fail-fast live encode error surfacing and resource cleanup for the v3.0.1 live-session contract. Depends on 0003. Status: **in progress**.
- [`0005-live-pattern-b-session-rewrite.md`](0005-live-pattern-b-session-rewrite.md) — drafted replacement plan for the video live session architecture rewrite: VTuber-aligned internal session routes, worker-owned Pattern B debit loop, authoritative worker-to-gateway HTTP events, and separation of runtime end from recording finalization. Depends on 0003. Status: **drafted**.
