# Running the daemon

## Quick reference

```
livepeer-video-worker-node \
  --gpu-vendor=nvidia \
  --config=/etc/livepeer/worker.yaml \
  --ffmpeg-bin=/usr/local/bin/ffmpeg \
  --presets-file=/etc/livepeer/presets/h264-streaming.yaml \
  --store-path=/var/lib/livepeer/transcode-state.db \
  --grpc-socket=/var/run/livepeer-video-worker.sock \
  --metrics-listen=:9091 \
  --payment-socket=/var/run/livepeer/payment.sock
```

## Runtime model

One daemon instance is expected to serve all three workload classes on
the same host:

| Workload | Behavior |
|---|---|
| `VOD` | One input → one output. Single-shot job submission via `POST /v1/video/transcode`. |
| `ABR` | One input → ladder of renditions + master HLS. Multiple ABR jobs may coexist; first cut keeps per-rendition sequencing within one ABR job. |
| `Live` | RTMP ingest at `:1935`; FFmpeg pipes to per-rendition HLS through the live session flow. |

The worker owns a shared GPU scheduler across all three. Batch workloads
may queue. Live has reserved headroom and fails fast when that reserved
capacity is exhausted.

Scheduler tuning knobs:

- `--gpu-slots` overrides detected total slot capacity
- `--gpu-live-reserved-slots` reserves slots for live admission
- `--gpu-cost-capacity` overrides total weighted cost budget
- `--gpu-live-reserved-cost` reserves weighted cost budget for live
- `--gpu-batch-cost-scale` scales preset-derived batch cost on this host
- `--gpu-live-cost-scale` scales preset-derived live cost on this host

Use the cost-scale flags when a host's real behavior is stricter or
looser than the default preset model, e.g. older consumer GPUs,
integrated Intel GPUs, or AMD variants with materially different encode
headroom.

## Live session knobs

```
livepeer-video-worker-node \
  --gpu-vendor=nvidia \
  --config=/etc/livepeer/worker.yaml \
  --presets-file=/etc/livepeer/presets/h264-streaming.yaml \
  --debit-cadence=5s \
  --stream-runway-seconds=30 \
  --stream-grace-seconds=60 \
  --stream-pre-credit-seconds=60 \
  --stream-restart-limit=3 \
  --stream-topup-min-interval=5s \
  --payment-socket=/var/run/livepeer/payment.sock
```

Broadcasters connect to `rtmp://transcode.example.com:1935/live/{stream_key}`.

## Dev mode (no GPU, no chain)

```
livepeer-video-worker-node --dev --presets-file=presets
```

`--dev` substitutes `FakeFFmpeg`, fake GPU profile (synthetic NVIDIA L40),
and fake payment broker. A loud `DEV MODE` banner prints to stderr.

## Docker (per-vendor runtime)

```
make docker-build DOCKER_TARGET=runtime-nvidia DOCKER_TAG=dev
make docker-build DOCKER_TARGET=runtime-intel  DOCKER_TAG=dev
make docker-build DOCKER_TARGET=runtime-amd    DOCKER_TAG=dev
```

## Environment + sockets

The worker expects the payment-daemon unix socket at the configured path:
- `--payment-socket` — `payment-daemon` (receiver mode), co-located on the same host.

Current compose/examples in this repo pin `livepeer-payment-daemon v4.0.1`
and use `/var/run/livepeer/payment.sock` to match the published image's
socket convention.

The worker reads shared `worker.yaml` via `--config` for its `worker`
section, optional top-level `auth_token`, optional top-level
`worker_eth_address`, and capability `offerings[]`. `payment_daemon:`
is owned by the co-located receiver daemon and should follow that
daemon's current schema (`recipient_eth_address`, `broker`, etc.). This
worker only requires the block to exist; it does not interpret those
daemon-specific fields itself.

`payment-daemon` is sourced from `livepeer-modules` and runs as a
separate process. The worker NEVER speaks chain RPC directly.

## Capabilities

The worker serves canonical capability data via `GET /registry/offerings`
derived from `worker.yaml`:

```
- video:transcode.vod
- video:transcode.abr
- video:live.rtmp
```

Orch-coordinator scrapes the worker by these strings and folds the
result into the operator-confirmed roster. See
[`../design-docs/worker-discovery.md`](../design-docs/worker-discovery.md).

## Observability

| Endpoint | Default | Notes |
|---|---|---|
| `GET /health` | `:8081` | Liveness + active work summary + GPU scheduler snapshot |
| `GET /registry/offerings` | `:8081` | Suite-wide capability advertisement for orch-coordinator scrape |
| `POST /v1/payment/ticket-params` | `:8081` | Bearer-authenticated helper that proxies receiver-side `GetTicketParams` |
| `GET /metrics` | `:9091` (off by default) | Prometheus; prefix `livepeer_videoworker_*`; includes slot and weighted-cost scheduler gauges |
| Operator gRPC | unix socket | `--grpc-socket=/var/run/...` |

## Cross-references

- [`../../README.md`](../../README.md) — module top-level overview.
- [`../../DESIGN.md`](../../DESIGN.md) — module architecture.
- [`../conventions/ports.md`](../conventions/ports.md) — port allocations.
- [`../conventions/metrics.md`](../conventions/metrics.md) — metric prefix + label conventions.
