---
id: 0007
slug: go-1-25-and-dependency-refresh
title: Upgrade worker toolchain to Go 1.25 and refresh pinned Go dependencies
status: completed
owner: agent
opened: 2026-05-02
depends-on: 0006
---

## Goal

Bring `video-worker-node` onto the newer Go toolchain and supporting
dependency line already used by `openai-worker-node`, so both worker
repos share the same baseline for CI, linting, vulnerability posture,
and generated binaries.

## Non-goals

- Changing the worker's HTTP, payment-daemon, or FFmpeg runtime
  contract.
- Reworking custom repo-specific lint rules or coverage policy beyond
  what is required for the toolchain bump.
- Introducing new runtime dependencies that are not already required by
  the worker.

## Cross-repo dependencies

- Reference only: `openai-worker-node` current baseline (`go 1.25.0`,
  `google.golang.org/grpc v1.80.0`,
  `google.golang.org/protobuf v1.36.11`,
  `golang.org/x/net v0.53.0`,
  `golang.org/x/sys v0.43.0`,
  `golang.org/x/text v0.36.0`).

## Approach

- [x] Audit every worker repo pin that encodes the Go toolchain line:
      `go.mod`, workflow `setup-go` versions, Docker build stages,
      Makefile comments, and docs that promise a specific Go floor.
- [x] Upgrade the worker to `go 1.25.x`, refresh the indirect Go
      dependency set (`x/net`, `x/sys`, `x/text`) via `go get` / `go mod
      tidy`, and keep already-aligned direct deps unchanged unless the
      toolchain requires a coordinated bump.
- [x] Re-run all worker quality gates after the bump:
      `make build`, `make test`, `make test-race`, `make lint`,
      `make coverage-check`, `make doc-lint`, and
      `golangci-lint run ./...`.
- [x] Sweep worker docs and workflows so the declared baseline matches
      the implemented one.

## Decisions log

### 2026-05-02 — Match the sibling worker on toolchain before widening scope
Reason: the OpenAI worker is the closest operational analogue for this
repo, so aligning first on Go/tooling versions reduces divergence
without forcing unrelated runtime changes.

### 2026-05-02 — Keep direct gRPC/protobuf pins stable unless the toolchain forces movement
Reason: `grpc` and `protobuf` are already aligned with the sibling
worker. The real gap is the Go line and its trailing indirect modules,
not the wire-level dependency set.

### 2026-05-02 — Restrict coverage-profile generation to packages with tests on Go 1.25
Reason: under this environment's Go 1.25 toolchain, `go test
-coverprofile=coverage.out ./...` fails on some no-test packages with
`go: no such tool "covdata"`. The repo already has a separate
per-package coverage gate that marks missing coverage as failures or
exemptions, so generating the profile only from packages with test files
preserves the quality bar while keeping the gate compatible with Go
1.25.

## Open questions

- None at completion of the upgrade pass.

## Artifacts produced

- `go.mod` bumped to `go 1.25.0` with refreshed indirect `x/*`
  dependencies.
- Worker GitHub Actions updated from Go `1.24` to `1.25`.
- `Makefile` coverage gate updated for Go 1.25 compatibility.
