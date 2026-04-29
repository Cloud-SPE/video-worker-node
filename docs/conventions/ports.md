---
title: Port allocation
status: accepted
last-reviewed: 2026-04-26
---

# Port allocation

What binds where. Detail on the worker's per-port auth model is in the
top-level [`DESIGN.md`](../../DESIGN.md) §"Trust boundaries".

## Public-facing

| Port | Component | Protocol |
|---|---|---|
| `8080` | shell (`apps/api`) | HTTPS (TLS at reverse proxy) |
| `1935` | worker | RTMP (per-worker) |
| `9094` | playback origin | HTTPS (TLS at reverse proxy or CDN) |

## Internal HTTP

| Port | Component | Notes |
|---|---|---|
| `8081` | worker | not public; only the shell calls it |

## Prometheus scrape (default off)

| Port | Component | Env to enable |
|---|---|---|
| `9090` | shell | `METRICS_LISTEN=:9090` |
| `9091` | worker | `--metrics-listen=:9091` |
| `9092` | payment-daemon | per `livepeer-modules` ports.md |
| `9091` | service-registry-daemon | per `livepeer-modules` ports.md (different host than worker, no collision) |

## Local IPC (unix sockets)

| Path | Owner | Notes |
|---|---|---|
| `/var/run/livepeer-payment-daemon.sock` | shell host (sender mode) and worker host (receiver mode) | same path, different hosts |
| `/var/run/livepeer-registry-daemon.sock` | shell host (resolver mode) and worker host (publisher mode) | same path, different hosts |
| `/var/run/livepeer-video-worker.sock` | worker | operator gRPC |

## Reserved for future modules

`9095–9099` reserved for future video-platform components (e.g., a future
ingest gateway, a future analytics ingest service, an SFU control plane).

## Compose vs production

Local `infra/compose.yaml`:
- exposes only what's needed for dev (`8080`, `1935`, `9094`, MinIO console
  `9001`, Postgres `5432` for inspect, Grafana `3000` once landed)
- metrics endpoints exposed for Prometheus scrape inside the compose network
- payment + registry sockets shared via named volume

Production:
- only `8080` (shell) and `1935` (worker) exposed publicly, behind reverse
  proxy with TLS termination
- metrics ports bound to private interface only
- unix sockets stay on host filesystem with restrictive perms

## Cross-references

- [`metrics.md`](metrics.md) — what gets emitted on those metrics ports
- [`../../DESIGN.md`](../../DESIGN.md) §"Trust boundaries" — auth model
