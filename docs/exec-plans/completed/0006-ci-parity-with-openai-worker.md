---
id: 0006
slug: ci-parity-with-openai-worker
title: Bring worker CI closer to the OpenAI worker baseline
status: completed
owner: agent
opened: 2026-05-02
depends-on: 0005
---

## Goal

Close the most important CI/release-process gaps between
`video-worker-node` and `openai-worker-node` without changing the
worker's runtime contract.

## Scope

- [x] Audit current workflow differences versus `openai-worker-node`.
- [x] Add missing repo-local workflow coverage where the gap is clear and
      low-risk: doc-lint placeholder / real hook, and Docker publish
      workflow parity.
- [x] Keep existing repo-specific checks (custom lints, coverage gate)
      intact.

## Decisions log

### 2026-05-02 — Similar does not mean identical
Reason: `video-worker-node` has different custom lint tooling and a
different Docker image matrix, so parity should be functional rather
than file-for-file.

### 2026-05-02 — Parity includes a separate golangci-lint job, not replacement of custom lints
Reason: the worker repo now mirrors the sibling worker's main lint
surface while preserving repo-specific invariant checks that do not fit
cleanly inside `golangci-lint`.

## Artifacts produced

- `.github/workflows/doc-lint.yml`
- `.github/workflows/docker.yml`
- `.github/workflows/lint.yml`
- `Makefile`
- `.golangci.yml`
