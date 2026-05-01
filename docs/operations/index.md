---
title: Operations index
status: drafted
last-reviewed: 2026-04-29
---

# Operations

Operator runbooks. How to run the daemon, what to watch, what to do when something is broken.

## Catalog

*(populates in [exec-plan 0001](../exec-plans/active/0001-extract-from-platform.md) Phase 3 — doc lift)*

- `running-the-daemon.md` — config layout (`worker.yaml`), startup sequence, how to pick a GPU build variant, and how worker `GET /registry/offerings` fits into the orch-coordinator flow.
- `troubleshooting.md` *(future)* — common failure modes: GPU mismatch, FFmpeg crash patterns, RTMP refused-connection, payment-daemon socket missing.
- `metrics-dashboard.md` *(future)* — example Grafana dashboard JSON for the worker's Prometheus output.

## Conventions

- Each runbook is **task-oriented**, not reference-oriented. ("How do I X?" not "What is X?".)
- Reference material (HTTP schemas, metric names) lives in `product-specs/` or `conventions/`.
