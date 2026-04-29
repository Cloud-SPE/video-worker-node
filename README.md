# video-worker-node

Go daemon that performs FFmpeg-subprocess transcoding (VOD / ABR / Live HLS) on Livepeer BYOC. Sister of [`openai-worker-node`](https://github.com/Cloud-SPE/openai-worker-node) — same scaffolding pattern, different workload.

> **Status: bootstrapping.** This repo is in [exec-plan 0001](docs/exec-plans/active/0001-extract-from-platform.md) Phase 1 — pillar docs only, no Go code yet. Build, test, and run instructions below become concrete after Phase 2 (the code lift).

## What it is

- **Workload-only** Go daemon. No `chain-commons`, no Stripe, no shell concerns.
- **Three runtime modes** (`--mode=vod|abr|live`) — pick one per process.
- **Three GPU build variants** (NVIDIA / Intel / AMD) — same source, three Docker tags.
- **Workload contracts:** HTTP `/v1/video/*` + `/stream/*` (`:8081`), RTMP ingest (`:1935`), Prometheus `/metrics` (`:9091`), gRPC into `livepeer-payment-daemon` (receiver) and `livepeer-service-registry-daemon` (publisher) over local unix sockets.

## Where to start

- [`AGENTS.md`](AGENTS.md) — table-of-contents for humans and agents
- [`DESIGN.md`](DESIGN.md) — architecture, layered domains, payment pipeline
- [`PRODUCT_SENSE.md`](PRODUCT_SENSE.md) — who consumes this, what good looks like, anti-goals
- [`PLANS.md`](PLANS.md) — how work is planned in this repo
- [`docs/references/openai-harness.pdf`](docs/references/openai-harness.pdf) — the harness-engineering philosophy this repo follows

## Build & run *(placeholder — fills in after Phase 2)*

```sh
# coming soon, mirroring openai-worker-node:
make build              # bin/livepeer-video-worker-node
make test               # go test ./...
make lint               # golangci-lint + custom lints
make doc-lint           # cross-link integrity + frontmatter freshness
make coverage-check     # 75% per-package gate
make proto              # regenerate vendored proto stubs
make docker-build DOCKER_TARGET=runtime-nvidia
docker compose up -d    # worker + payment-daemon + service-registry-daemon
```

## Repository layout *(target shape after Phase 2)*

```
.
├── AGENTS.md                  # the map
├── DESIGN.md                  # architecture
├── PRODUCT_SENSE.md           # who/what/why
├── PLANS.md                   # plan format & lifecycle
├── README.md                  # you are here
├── cmd/livepeer-video-worker-node/   # entrypoint (Phase 2)
├── internal/                          # (Phase 2)
│   ├── types/         config/    repo/
│   ├── service/       runtime/
│   └── providers/
├── proto/                             # (Phase 2)
├── presets/                           # encoding ladders (Phase 2)
├── lint/                              # custom Go lints (Phase 2)
├── docs/
│   ├── design-docs/
│   ├── exec-plans/{active,completed}/
│   ├── product-specs/
│   ├── operations/
│   ├── conventions/
│   ├── generated/
│   └── references/
└── .github/workflows/                 # (Phase 2)
```

## License

Not yet decided — see [`docs/exec-plans/active/0001-extract-from-platform.md`](docs/exec-plans/active/0001-extract-from-platform.md) decision **D6**. Until a `LICENSE` file lands, default US copyright applies (all rights reserved).
