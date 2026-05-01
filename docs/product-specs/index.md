---
title: Product specs index
status: drafted
last-reviewed: 2026-04-29
---

# Product specs

External-facing contracts the consuming shell relies on. Changing anything here is a **protocol change** and requires an exec-plan plus coordination with whichever shell is downstream.

## Catalog

*(populates in [exec-plan 0001](../exec-plans/active/0001-extract-from-platform.md) Phase 3 — doc lift)*

- `http-api.md` — HTTP surface: `/health`, `/registry/offerings`, `POST /v1/video/transcode`, `POST /v1/video/transcode/abr`, `POST /stream/start`, `POST /stream/stop`. Request/response schemas, error codes, header conventions.
- `grpc-surface.md` — gRPC operator socket. Operational RPCs (drain, shutdown, capability refresh).

## Not here

- The bridge-pattern dispatch contract (how the shell discovers + dispatches to a worker) is shell-side and lives in whichever shell repo consumes this worker.
- Internal worker → shell webhook callback paths (`/internal/live/*`) are documented in `docs/conventions/webhook-signing.md` and detailed in `docs/design-docs/internal-callback-api.md`.
