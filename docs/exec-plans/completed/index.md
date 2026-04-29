---
title: Completed exec-plans
status: drafted
last-reviewed: 2026-04-29
---

# Completed exec-plans

Archived plans authored in this repo. **Do not modify.** History is immutable.

## Catalog

- [`0001-extract-from-platform.md`](0001-extract-from-platform.md) — Lift the worker out of the `livepeer-video-platform` monorepo into this standalone repo. Closed 2026-04-29 across four phases (bootstrap → code lift → doc lift → verification + final wiring). Inherited three pre-existing source bugs as documented tech debt: `internal/runtime/metrics` data race, `lint/doc-gardener` no-op stub, stale `proto/buf.yaml` `livepeer/transcode/v1` module.

## What is *not* here

Plans authored in `livepeer-video-platform` for its `apps/transcode-worker-node/` (e.g. that monorepo's plans `0002` and `0006`) are **not lifted into this repo**, per [`0001-extract-from-platform.md`](0001-extract-from-platform.md) decision **D3**. Their outcomes are encoded in the lifted code (Phase 2) and design-docs (Phase 3); the trial-and-error of those plans is monorepo archaeology we deliberately do not import.
