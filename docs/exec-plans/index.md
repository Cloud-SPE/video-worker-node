---
title: Exec-plans index
status: drafted
last-reviewed: 2026-04-29
---

# Exec-plans

Plans are first-class artifacts. Format and lifecycle: [`PLANS.md`](../../PLANS.md).

## Subdirectories

| Path | Purpose |
|---|---|
| [`active/`](active/index.md) | In-flight plans. Each is a contract between humans (steer) and agents (execute). |
| [`completed/`](completed/index.md) | Archived plans authored in this repo. **Do not modify.** |
| [`tech-debt-tracker.md`](tech-debt-tracker.md) | Append-only log of known shortcuts and deferrals. |

## Counter

Plan IDs are monotonic, zero-padded to 4 digits, **unique within this repo**. Plans 0001 (extraction, completed) and 0002 (v3 archetype-A alignment, active) are taken; the next plan to be authored is `0003`.

Plans authored in `livepeer-video-platform` for its `apps/transcode-worker-node/` are **not lifted** into this repo (decision **D3** in plan 0001). Their outcomes are encoded in the lifted code + design-docs; the plans themselves stay in the monorepo.
