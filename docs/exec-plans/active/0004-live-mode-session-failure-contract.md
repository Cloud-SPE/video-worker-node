---
id: 0004
slug: live-mode-session-failure-contract
title: Live mode session failure contract
status: drafted
owner: agent
opened: 2026-05-01
depends-on: 0003
---

## Goal
Define and implement the v3.0.1 live-mode failure behavior required by
the updated specs: when an in-progress live encode fails, the worker
must fail fast, surface a structured `session_worker_failed` outcome,
and clean up FFmpeg / RTMP resources without attempting migration.

## Non-goals
- Stateful live-session migration.
- New ingest protocols such as SRT or WHIP.
- Reworking the live payment cadence model beyond what is needed to
  surface failure cleanly and avoid leaks.

## Cross-repo dependencies
- `0003-v3-0-1-worker-contract-alignment.md` establishes the shared
  config and HTTP surface this plan builds on.
- Gateway-side handling of the failure code lives outside this repo, but
  this repo must emit the agreed contract.

## Approach
- [ ] Trace every live encode failure path in `internal/service/liverunner/`
      and `internal/providers/ingest/rtmp/` to identify where the
      current code already emits a structured terminal state versus where
      it only logs / tears down implicitly.
- [ ] Introduce a single worker-side failure code path for live session
      encode failures (`session_worker_failed`) and map internal FFmpeg /
      ingest / storage failures onto it without losing operator-useful
      logs.
- [ ] Ensure teardown is explicit and idempotent: FFmpeg subprocess,
      ingest session, temp storage, and any ticker/payment loop state
      must be released on failure.
- [ ] Add tests for at least FFmpeg crash, ingest disconnect, and
      cleanup-on-close behavior.
- [ ] Document the live failure semantics in `DESIGN.md` and the
      relevant product/design docs.

## Decisions log

### 2026-05-01 — Live failure behavior is isolated in its own plan
Reason: the main v3.0.1 alignment work already spans config, proto,
HTTP, docs, and deploy artifacts. Live failure surfacing is materially
different work with a distinct risk profile and should be tracked
separately even if executed in the same release wave.

## Open questions
- Exact outward surface for the structured failure in the current live
  APIs: whether the authoritative signal is the HTTP session/status path,
  an internal callback, or both.
- Whether any existing shell-facing callback/event names should be
  normalized at the same time, or whether this repo should only map to
  the new error code and leave event taxonomy unchanged.

## Artifacts produced
- None yet.
