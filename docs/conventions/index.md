---
title: Conventions index
status: drafted
last-reviewed: 2026-04-29
---

# Conventions

Repo-wide naming and contract rules. **Cross-cutting**: changes here ripple through all of `internal/` and need a design-doc + exec-plan.

## Catalog

*(populates in [exec-plan 0001](../exec-plans/active/0001-extract-from-platform.md) Phase 3 — doc lift, narrowed to worker scope)*

- `metrics.md` — Prometheus metric naming (prefix `livepeer_videoworker_*`), label conventions, cardinality rules.
- `ports.md` — port allocation: `:1935` RTMP ingest, `:8081` HTTP, `:9091` Prometheus.
- `webhook-signing.md` — `X-Video-Signature: sha256=<hex>` header, HMAC scheme, retry/backoff policy.

## Why "narrowed to worker scope"

The monorepo's `docs/conventions/` covers the whole platform (shell, worker, playback origin). Lifting verbatim would carry shell-only conventions (rate-limiting, customer auth, signed playback URLs, etc.) that don't apply here. Phase 3 of plan 0001 lifts only the conventions the worker actually participates in.
