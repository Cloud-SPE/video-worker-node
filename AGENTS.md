# AGENTS.md — video-worker-node

This repository hosts `livepeer-video-worker-node`: the Go daemon that performs FFmpeg-subprocess transcoding (VOD / ABR / Live HLS) on a host with GPU capacity. It validates payments via a sidecar `livepeer-payment-daemon` (receiver mode) and exposes `GET /registry/offerings` for orch-coordinator scrape. `payment-daemon` is consumed as a published container image from `livepeer-modules`, never as a source dependency. Workload-only: no `chain-commons`, no Stripe, no shell concerns.

**Humans steer. Agents execute. Scaffolding is the artifact.**

## Start here

- Architecture: [DESIGN.md](DESIGN.md)
- Product mental model: [PRODUCT_SENSE.md](PRODUCT_SENSE.md)
- How to plan work: [PLANS.md](PLANS.md)
- Harness philosophy: [docs/references/openai-harness.pdf](docs/references/openai-harness.pdf)
- Active v3 alignment plan: [docs/exec-plans/active/0003-v3-0-1-worker-contract-alignment.md](docs/exec-plans/active/0003-v3-0-1-worker-contract-alignment.md)

## Knowledge base layout

- `docs/design-docs/` — accepted design decisions (`index.md` is the entry)
- `docs/exec-plans/active/` — in-flight work with progress logs
- `docs/exec-plans/completed/` — archived plans; do not modify
- `docs/exec-plans/tech-debt-tracker.md` — known debt, append-only
- `docs/product-specs/` — HTTP + gRPC contracts the shell relies on
- `docs/operations/` — operator runbooks (running the daemon, troubleshooting)
- `docs/conventions/` — repo-wide rules (metrics prefix, port allocation, webhook signing)
- `docs/generated/` — auto-generated; never hand-edit
- `docs/references/` — external material (harness PDF, lifted-from-source)

## The layer rule (non-negotiable)

Source under `internal/` follows a strict forward-only stack:

```
types → config → repo → service → runtime
                   │       │
                   ▼       ▼
              providers (cross-cutting; single hop)
```

Cross-cutting concerns (FFmpeg subprocess, GPU probe, RTMP ingest, HLS writer, storage upload, payment-daemon gRPC, webhooks, metrics, logger, clock, store) enter through a single layer: `internal/providers/`. Nothing in `service/` may import `os/exec`, a logging library, an HTTP client, etc. directly — only through a `providers/` interface.

Lints enforce this in CI. See `docs/design-docs/architecture.md` (lifts in Phase 3).

## Three runtime modes

The daemon runs in one of three modes (`--mode=`):

- **`vod`** — one input → one output. `internal/service/jobrunner/`.
- **`abr`** — one input → ladder of renditions + master HLS. `internal/service/abrrunner/`.
- **`live`** — persistent FFmpeg fed by RTMP ingest. NVIDIA-only at MVP. `internal/service/liverunner/`.

Per-mode wiring is exclusive: the unused runners are nil and the HTTP layer rejects their paths with `501 Not Implemented`.

## Three GPU build variants

Same source, three Docker targets: `runtime-nvidia`, `runtime-intel`, `runtime-amd`. The `gpu` provider probes the runtime environment (`nvidia-smi` / `vainfo`) and refuses to start if the build's vendor mismatches the host. Live mode is NVIDIA-only at MVP.

## Toolchain

- Go 1.25+ (matches sibling worker baseline)
- `buf` for regenerating proto stubs (`make proto`); sources live in `proto/livepeer/`
- `golangci-lint` + custom lints in `lint/`
- FFmpeg as a subprocess — never linked, never bound (no cgo)

## Commands

> Concrete commands land with Phase 2 of plan 0001 (the code lift). Until then, the harness is prose-only.

Anticipated targets, mirroring the source module:

- `make build` — build `bin/livepeer-video-worker-node`
- `make test` / `make test-race` — unit tests
- `make lint` — `golangci-lint` + custom lints
- `make doc-lint` — knowledge-base cross-link integrity + frontmatter freshness
- `make coverage-check` — 75% per-package gate
- `make proto` — regenerate vendored proto stubs

## Invariants (do not break without a design-doc)

1. **No `chain-commons`.** The worker is portable across payment systems. Hard-fail lint.
2. **No cgo.** FFmpeg is a subprocess, never a binding. Hard-fail lint.
3. **No `os/exec` outside `providers/ffmpeg` + `providers/gpu`.** `golangci-lint` depguard.
4. **No `prometheus/client_golang` outside `providers/metrics`.** Same.
5. **No `go-rtmp` outside `providers/ingest/rtmp`.** Same — the provider boundary owns the wire-protocol library.
6. **No secrets in logs.** Stream keys are redacted to a 6-char prefix via `redactKey()`. Lint enforces.
7. **No customer concepts.** API keys, projects, billing, customer-facing webhooks belong to the shell, not here.
8. **Test coverage ≥ 75% per package.** CI fails below this threshold; per-package `exemptions.txt` allowed but each entry needs a one-line justification.
9. **No code without a plan.** Non-trivial work starts with an entry in `docs/exec-plans/active/`.

## Where to look for X

| Question | Go to |
|---|---|
| What does the worker do? | [DESIGN.md](DESIGN.md) |
| Why is X done this way? | `docs/design-docs/` |
| What's in flight? | `docs/exec-plans/active/` |
| What HTTP routes does it serve? | `docs/product-specs/http-api.md` (lifts in Phase 3) |
| What gRPC surface does it expose? | `docs/product-specs/grpc-surface.md` (lifts in Phase 3) |
| How do I run the daemon? | `docs/operations/running-the-daemon.md` (lifts in Phase 3) |
| Known debt? | `docs/exec-plans/tech-debt-tracker.md` |

## Cross-repo context

| Repo / image | Role |
|---|---|
| [`livepeer-modules`](https://github.com/Cloud-SPE/livepeer-modules) | Source of the receiver-mode `payment-daemon` Docker image and the canonical payments proto contract. NEVER modified or imported as source. Proto stubs are vendored under `proto/livepeer/` and regenerated via `make proto`. |
| Sister: [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node) | Same scaffolding pattern, AI/inference workload. Reference for Go conventions and the harness shape. |
| Consuming shell | Any video gateway that speaks the bridge protocol (HTTP `POST /v1/video/transcode` + `livepeer-payment` ticket header, RTMP ingest at `:1935`). The reference shell is Cloud-SPE's video gateway; nothing in this repo is shell-specific. |
