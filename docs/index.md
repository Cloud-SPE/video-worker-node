---
title: docs/ — system of record
status: drafted
last-reviewed: 2026-04-29
---

# docs/ — system of record

Per the harness PDF: this directory is the **system of record** for the repo. Anything an agent or human needs to know about the worker should be discoverable from here. If it isn't in `docs/`, the agent can't see it.

The repo's [`AGENTS.md`](../AGENTS.md) is the **table of contents**; this directory holds the actual content.

## Subdirectories

| Path | Purpose |
|---|---|
| [`design-docs/`](design-docs/index.md) | Accepted design decisions. One file per decision. |
| [`exec-plans/`](exec-plans/index.md) | Plans for in-flight and completed work, plus the tech-debt tracker. |
| [`product-specs/`](product-specs/index.md) | External-facing contracts (HTTP, gRPC) the consuming shell relies on. |
| [`operations/`](operations/index.md) | Operator runbooks. |
| [`conventions/`](conventions/index.md) | Repo-wide naming + contract rules (metrics prefix, ports, webhook signing). |
| [`generated/`](generated/index.md) | Auto-produced docs. Never hand-edit. |
| [`references/`](references/index.md) | External material brought in-repo so it's legible to agents. |

## Status

Phase 1 of [exec-plan 0001](exec-plans/active/0001-extract-from-platform.md) lays down this skeleton. Phase 3 lifts content from the `livepeer-video-platform` monorepo's worker into these subdirectories. Until Phase 3 runs, most index pages enumerate the docs that **will** land, not docs that exist now.
