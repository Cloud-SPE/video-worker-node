---
title: Metrics conventions
status: accepted
last-reviewed: 2026-04-26
---

# Metrics conventions

Adapted from `livepeer-modules/docs/conventions/metrics.md`. Scoped
to this worker. Cross-stack siblings (`payment-daemon`,
`service-registry-daemon`) keep their own prefixes per their repo's
convention; we don't override those.

## Prefixes

| Prefix | Component |
|---|---|
| `videogateway_*` | shell (`apps/api/`) |
| `videocore_*` | engine (`packages/video-core/`) — only relevant when engine emits its own metrics (it can; ships an optional `Recorder` interface) |
| `livepeer_videoworker_*` | worker (`apps/transcode-worker-node/`) |
| `livepeer_payment_*` | payment-daemon (sourced from `livepeer-modules`; we don't change) |
| `livepeer_registry_*` | service-registry-daemon (sourced; we don't change) |

## Mandatory labels

Every metric the platform emits carries at least:

| Label | Required when | Values |
|---|---|---|
| `capability` | always (engine + shell + worker) | one of the canonical `Capability` strings |
| `status` | for any operation that can succeed/fail | `success | error | timeout | rejected` |
| `codec` | for transcode-related metrics | `h264 | hevc | av1` |
| `gpu_vendor` | for worker metrics | `nvidia | intel | amd | none` |

Optional:

- `tier` — caller tier (`free | pro | enterprise`) for quota-relevant metrics.
- `error_code` — short identifier for error histograms.

## No high-cardinality labels

NEVER use as a label:

- `caller_id` / `project_id` / `user_id` (high cardinality; explosive)
- `asset_id` / `live_stream_id` / `playback_id`
- `worker_url` (high churn; use `worker_id` if needed)
- `request_path` raw (use route templates: `/v1/assets/:id` not `/v1/assets/abc123`)
- IP addresses
- Email addresses
- API keys / stream keys (also: NEVER log these; lint enforces)

## Cardinality cap

Engine ships a `Recorder` interface with a built-in cardinality cap (default
1000 series per metric name). Shell wires a `prom-client` decorator that
enforces it before emitting. Configurable via env if a deployment legitimately
needs higher.

## No `prom-client` outside the metrics package

Lint enforces: only `apps/api/src/providers/metrics/` (and the worker's
`internal/providers/metrics/`) imports `prom-client` (TS) or
`github.com/prometheus/client_golang/*` (Go). Service / dispatch / runtime /
repo layers call the abstract `Recorder` interface.

## Metric types

Default to:

| Pattern | Type |
|---|---|
| count of events (`*_total`) | counter |
| in-flight value (`*_inflight`) | gauge |
| current value (`*_seconds`, `*_bytes`) | gauge |
| latency distribution (`*_duration_seconds`) | histogram |
| size distribution (`*_size_bytes`) | histogram |

Histograms with default buckets. Custom buckets only when justified.

## Headline metrics (MVP minimum)

Each component emits at least:

### Shell (`videogateway_*`)

- `videogateway_http_requests_total{capability, route, method, status}` (counter)
- `videogateway_http_request_duration_seconds{capability, route, method, status}` (histogram)
- `videogateway_dispatcher_requests_total{capability, status}` (counter)
- `videogateway_wallet_reservations_total{capability, status}` (counter)
- `videogateway_wallet_reserved_cents{tier}` (gauge)
- `videogateway_wallet_committed_cents_total{capability}` (counter)
- `videogateway_webhook_deliveries_total{event_type, status}` (counter)
- `videogateway_webhook_delivery_duration_seconds{event_type, status}` (histogram)
- `videogateway_webhook_pending_count` (gauge)
- `videogateway_db_connections_inflight` (gauge)
- `videogateway_app_build_info{version, sha, build_date}` (gauge, value=1)

### Worker (`livepeer_videoworker_*`)

- `livepeer_videoworker_jobs_total{capability, codec, status, gpu_vendor}` (counter)
- `livepeer_videoworker_job_duration_seconds{capability, codec, gpu_vendor, status}` (histogram)
- `livepeer_videoworker_encode_fps{capability, codec, resolution, gpu_vendor}` (gauge)
- `livepeer_videoworker_ffmpeg_crashes_total{gpu_vendor}` (counter)
- `livepeer_videoworker_gpu_sessions_inflight{gpu_vendor}` (gauge)
- `livepeer_videoworker_rtmp_connections_inflight` (gauge)
- `livepeer_videoworker_streaming_session_inflight` (gauge)
- `livepeer_videoworker_streaming_session_debits_total{status}` (counter)
- `livepeer_videoworker_storage_uploads_total{status}` (counter)
- `livepeer_videoworker_app_build_info{version, sha, build_date}` (gauge, value=1)

## Histograms with realistic buckets

For HTTP latency: `[0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10]`
seconds.

For job durations: depends on the job. Encode jobs of 1080p 1h source can
take 30+ minutes. Buckets: `[1, 5, 15, 30, 60, 120, 300, 600, 1200, 1800, 3600]`
seconds.

For payload sizes: `[1KB, 10KB, 100KB, 1MB, 10MB, 100MB, 1GB]`.

## Build info metric pattern

Every component exports `<prefix>_app_build_info{version, sha, build_date}`
with value `1`. Lets dashboards show "what version is deployed" without
running container introspection.

## Prom name discipline

- Lowercase, snake_case. No camelCase.
- Counters end in `_total`.
- Durations end in `_seconds` (Prometheus base unit).
- Sizes end in `_bytes`.
- `inflight` for current-in-flight gauges.

## Cross-references

- [`ports.md`](ports.md) — where each component's metrics endpoint binds.
- [`../../DESIGN.md`](../../DESIGN.md) §"Trust boundaries" — auth model for
  the metrics endpoints (none; default off; reverse-proxy for restriction).
