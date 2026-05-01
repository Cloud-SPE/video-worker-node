# Running the daemon

## Quick reference

```
livepeer-video-worker-node \
  --mode=vod \
  --gpu-vendor=nvidia \
  --config=/etc/livepeer/worker.yaml \
  --ffmpeg-bin=/usr/local/bin/ffmpeg \
  --presets-file=/etc/livepeer/presets/h264-vod.yaml \
  --store-path=/var/lib/livepeer/transcode-state.db \
  --grpc-socket=/var/run/livepeer-video-worker.sock \
  --metrics-listen=:9091 \
  --payment-socket=/var/run/livepeer-payment-daemon.sock
```

## Modes

| Flag | Behavior |
|---|---|
| `--mode=vod` | One input → one output. Single-shot job submission via `POST /v1/video/transcode`. |
| `--mode=abr` | One input → ladder of renditions + master HLS. Sequential per-rendition encode + per-rendition payment debit. |
| `--mode=live` | RTMP ingest at `:1935`; FFmpeg pipes to per-rendition HLS. NVIDIA-only at MVP. Implementation in `internal/service/liverunner/` and `internal/service/livecdn/`. |

## Live mode

```
livepeer-video-worker-node \
  --mode=live \
  --gpu-vendor=nvidia \
  --config=/etc/livepeer/worker.yaml \
  --presets-file=/etc/livepeer/presets/h264-live.yaml \
  --debit-cadence=5s \
  --stream-runway-seconds=30 \
  --stream-grace-seconds=60 \
  --stream-pre-credit-seconds=60 \
  --stream-restart-limit=3 \
  --stream-topup-min-interval=5s \
  --payment-socket=/var/run/livepeer-payment-daemon.sock
```

Broadcasters connect to `rtmp://transcode.example.com:1935/live/{stream_key}`.

## Dev mode (no GPU, no chain)

```
livepeer-video-worker-node --mode=vod --dev --presets-file=presets/h264-vod.yaml
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

The worker reads shared `worker.yaml` via `--config` for its `worker`
section, optional top-level `auth_token`, optional top-level
`worker_eth_address`, and capability `offerings[]`. `payment_daemon:`
is shared with the co-located receiver daemon, but this worker only
consumes its own worker-facing fields plus the capability catalog.

`payment-daemon` is sourced from `livepeer-modules` and runs as a
separate process. The worker NEVER speaks chain RPC directly.

## Capabilities

The worker serves canonical capability data via `GET /registry/offerings`
derived from `worker.yaml`:

```
- video:transcode.vod
- video:transcode.abr
- video:live.rtmp     (when --mode=live)
```

Orch-coordinator scrapes the worker by these strings and folds the
result into the operator-confirmed roster. See
[`../design-docs/worker-discovery.md`](../design-docs/worker-discovery.md).

## Observability

| Endpoint | Default | Notes |
|---|---|---|
| `GET /health` | `:8081` | Liveness + mode + active stream count |
| `GET /registry/offerings` | `:8081` | Suite-wide capability advertisement for orch-coordinator scrape |
| `GET /metrics` | `:9091` (off by default) | Prometheus; prefix `livepeer_videoworker_*` |
| Operator gRPC | unix socket | `--grpc-socket=/var/run/...` |

## Cross-references

- [`../../README.md`](../../README.md) — module top-level overview.
- [`../../DESIGN.md`](../../DESIGN.md) — module architecture.
- [`../conventions/ports.md`](../conventions/ports.md) — port allocations.
- [`../conventions/metrics.md`](../conventions/metrics.md) — metric prefix + label conventions.
