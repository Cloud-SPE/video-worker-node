# DESIGN.md — video-worker-node

> Top-level architecture summary. Detailed design lives under `docs/design-docs/`. This file should still make sense after a refactor; it captures the *shape*, not the implementation.

## What this service is

`livepeer-video-worker-node` is the payee-side video adapter in the Livepeer BYOC payment architecture. It accepts transcode requests over HTTP (or persistent RTMP ingest for live mode), validates the attached payment via a co-located `livepeer-payment-daemon` (receiver mode), spawns FFmpeg as a subprocess, writes HLS output to S3-compatible storage, and reports completion via signed webhooks back to whichever shell dispatched the work.

There is no transcoding network, no orchestrator pool, no on-chain interaction in this process. The worker exposes `GET /registry/offerings` for orch-coordinator scrape, accepts paid work through the `livepeer-payment` HTTP header, and exposes `POST /v1/payment/ticket-params` as a thin authenticated proxy to the co-located receiver daemon's `GetTicketParams` RPC.

The worker is **workload-only**: portable across payment systems, agnostic to which shell consumes it, with zero customer-facing concepts (API keys, projects, billing, customer webhooks all live in the shell).

## Position in the stack

```
┌─────────────────┐     HTTPS       ┌──────────────────────────────────────┐
│  Video shell    │  POST /v1/...   │ video-worker-node  (this repo)       │
│  (any gateway   │ ─────────────▶  │  ┌────────────────────────────────┐  │
│   that speaks   │                 │  │ HTTP /v1/* + /stream/*  :8081  │  │
│   the bridge    │ ◀───────────── │  │ RTMP ingest             :1935  │  │
│   protocol)     │   webhook       │  │ gRPC operator socket           │  │
└────────┬────────┘   callbacks     │  │ Prometheus /metrics     :9091  │  │
         │                          │  └────────┬─────────────┬─────────┘  │
         │ resolve(capability)      │     gRPC  │             │            │
         │ via coordinator roster   │   (unix)  │             │ subprocess │
         ▼                          │           ▼             ▼            │
┌─────────────────┐                 │  ┌────────────────┐  ┌──────────┐    │
│  orch-          │                 │  │ payment-daemon │  │ ffmpeg / │    │
│  coordinator    │                 │  │ (receiver)     │  │ ffprobe  │    │
│  + signed roster│                 │  │                │  │          │    │
└─────────────────┘                 │  └────────────────┘  └────┬─────┘    │
                                    │                           │          │
                                    │                           │ HLS      │
                                    │     GET /registry/offerings▼ segments │
                                    │                         ┌──────────┐  │
                                    │                         │ S3-      │  │
                                    │                         │ compat.  │  │
                                    │                         │ storage  │  │
                                    │                         └──────────┘  │
                                    └──────────────────────────────────────┘
```

The two daemons are sister images from `livepeer-modules`. They run as containers next to this worker in `compose.yaml`; this worker never modifies or forks them, only consumes their proto stubs (vendored under `proto/livepeer/`, regenerated via `make proto`).

## Layered domain architecture

Per the harness convention, code under `internal/` flows forward only:

```
┌─────────────────────────────────────────────────────────────────┐
│              cmd/livepeer-video-worker-node                     │
│        (flag parsing, provider wiring, signal context)          │
└────────────────────────────────┬────────────────────────────────┘
                                 │
              ┌──────────────────┴──────────────────┐
              ▼                                     ▼
   ┌─────────────────────┐               ┌──────────────────────┐
   │   internal/runtime  │               │   internal/service   │
   │  ┌──────┬──────┐    │               │  ┌─────────────────┐ │
   │  │ http │ grpc │    │               │  │ jobrunner   VOD │ │
   │  └──────┴──────┘    │               │  │ abrrunner   ABR │ │
   │  metrics, lifecycle │               │  │ liverunner Live │ │
   └──────────┬──────────┘               │  │ preflight       │ │
              │                          │  │ paymentbroker   │ │
              │                          │  │ presetloader    │ │
              │                          │  │ livecdn         │ │
              │                          │  └────────┬────────┘ │
              │                          └───────────┼──────────┘
              ▼                                      ▼
   ┌──────────────────────────────────────────────────────────┐
   │              internal/repo/jobs                          │
   │   (durable job + stream records via Store provider)      │
   └────────────────────────────┬─────────────────────────────┘
                                ▼
   ┌──────────────────────────────────────────────────────────┐
   │              internal/providers                          │
   │ ffmpeg (subprocess) │ gpu (nvidia-smi / vainfo)          │
   │ probe (ffprobe)     │ storage (HTTP GET/PUT pre-signed)  │
   │ webhooks (HMAC)     │ store (BoltDB / Memory)            │
   │ hls (master.m3u8)   │ ingest/rtmp (go-rtmp; no cgo)      │
   │ paymentclient   ─── gRPC unix ───▶ payment-daemon        │
   │ logger, metrics, clock, thumbnails, filters, progress    │
   └────────────────────────────┬─────────────────────────────┘
                                ▼
   ┌──────────────────────────────────────────────────────────┐
   │                   internal/types                         │
   │   Mode, Job, Stream, Preset, GPUProfile, errors          │
   └──────────────────────────────────────────────────────────┘
```

`runtime/` wires `providers/` + `service/` into HTTP / gRPC servers. Nothing else imports `providers/`. `service/` contains pure business logic and is unit-testable without a subprocess, network, or filesystem.

## Per-mode wiring

The daemon runs in **exactly one** mode per process; the unused runners are nil and the HTTP layer rejects their paths with `501 Not Implemented`.

- **`--mode=vod`** — `jobrunner` is wired. One input → one output. Simple end-to-end FFmpeg invocation; payment debited per completed job.
- **`--mode=abr`** — `abrrunner` is wired. One input → ladder of renditions + master HLS. Sequential per-rendition encode, per-rendition payment debit, master manifest assembly at the end.
- **`--mode=live`** — `liverunner` is wired. Persistent FFmpeg subprocess fed by RTMP ingest, streaming-session payment pattern, recording-bridge handoff to `livecdn`. NVIDIA-only at MVP.

## Three GPU build variants

Same Go source, three Docker targets:

| Vendor | Target | FFmpeg flags | Runtime probe |
|---|---|---|---|
| NVIDIA | `runtime-nvidia` | `-hwaccel cuda -c:v h264_nvenc` | `nvidia-smi` |
| Intel | `runtime-intel` | `-hwaccel vaapi -c:v h264_vaapi` | `vainfo` |
| AMD | `runtime-amd` | `-hwaccel vaapi -c:v h264_vaapi` | `vainfo` |

The `gpu` provider probes the runtime environment at startup and refuses to start if the build vendor and host hardware mismatch — fail-closed, not fall-through.

## The payment pipeline (one VOD request)

```
HTTP POST /v1/video/transcode                              (livepeer-payment ticket)
  │
  ▼
runtime/http  ── payment middleware ─┐
  │                                  │
  │              ProcessPayment(ticket, work_id)   ─gRPC─▶ payment-daemon
  │                                  ◀── { sender, credited_ev }
  │                                  │
  │              estimator(input, preset) → est_units
  │                                  │
  │              DebitBalance(sender, work_id, est_units) ─gRPC─▶ payment-daemon
  │                                  ◀── { balance ≥ 0 }
  │                                  │
  ▼
service/jobrunner.Run               ── subprocess ──▶ ffmpeg
  │                                  │
  │                                  ▼ HLS segments
  │                                providers/storage.Put ─PUT─▶ object storage
  │                                  │
  │              actual_units = meter(probe + duration)
  │              if actual > est: DebitBalance(delta)
  │                                  │
  │              providers/webhooks.Send  ─HMAC HTTP─▶ shell callback URL
  ▼
HTTP response complete
```

Reconciliation is over-debit only: if `actual < est` the ledger stays ahead; we never credit back. This matches the openai-worker-node convention.

Live mode follows the **streaming-session pattern**: one `ProcessPayment` per session, periodic `DebitBalance` calls keyed to RTMP keepalive. Detailed in `docs/design-docs/streaming-session-live-mode.md` (lifts in Phase 3).

## Cross-process contracts

### Payment-daemon gRPC

Sources live in `proto/livepeer/payments/v1/`; generated Go in `proto/gen/go/...`. The `.proto` files are wire-compatible copies of the daemon's; regenerate with `make proto`. This repo consumes the `PayeeDaemonClient` — it does not implement the service.

### Registry surface

Workers are registry-invisible under archetype A. The worker exposes a
uniform `/registry/offerings` HTTP endpoint that orch-coordinator
scrapes. Operator-curated roster + secure-orch console signing replaces
the old worker-self-publishing flow. Vendored proto sources at
`proto/livepeer/registry/v1/` remain as adjacent contract material; no
registry gRPC calls go out from this binary.

### Bridge HTTP contract

Endpoints exposed (defined in `docs/product-specs/http-api.md`, lifts in Phase 3):

- `GET /health` — liveness + `protocol_version` + `inflight`
- `GET /registry/offerings` — suite-wide worker advertisement surface
- `POST /v1/payment/ticket-params` — authenticated payee-side ticket-params helper
- `POST /v1/video/transcode` — VOD work, paid
- `POST /v1/video/transcode/abr` — ABR ladder work, paid
- `POST /stream/start` — open a live session, paid
- `POST /stream/stop` — close a live session

### RTMP ingest

`rtmp://host:1935/live/{stream_key}` — stream key validated against the active live session opened via `POST /stream/start`.

### Webhooks (worker → shell)

HMAC-SHA256 signed via `X-Video-Signature: sha256=<hex>`, delivered with exponential backoff. Used for state transitions: `transcode.complete`, `stream.live`, `stream.recording_finalized`, etc.

## Trust boundaries

| Surface | Bind | Auth |
|---|---|---|
| HTTP `/v1/*`, `/stream/*` `:8081` | TCP, configurable | Optional bearer token + payment ticket validation |
| RTMP ingest `:1935` | TCP (public) | Stream key in URL path, validated against active session |
| gRPC operator socket | unix only | Filesystem permissions on the socket file |
| Prometheus `/metrics` `:9091` | TCP, off by default | Unauthenticated; reverse-proxy for auth |
| Outbound to `payment-daemon` | unix socket | Local trust |
| Outbound webhook callbacks | TCP | HMAC-SHA256 body signature (`X-Video-Signature`) |
| Outbound to object storage | TCP | Pre-signed URLs from the shell, scoped per job |

## Explicit non-goals (worker scope)

- No fan-out / load-balancing across multiple FFmpeg instances per process. One job at a time per concurrency slot.
- No authn/authz beyond the payment header. **Payment IS auth.**
- No rate limiting beyond per-sender balance exhaustion.
- No hot config reload. Restart the process to change config.
- No credit-back primitive. Over-debit is final.
- No customer concepts. Every customer-facing concern (API keys, projects, billing, customer webhooks, signed playback URLs) is the shell's job.
- No DRM. Public + signed-token playback only — the shell signs, the worker outputs plaintext segments.
- No managed CDN. The worker writes HLS to object storage; edge delivery is downstream of the worker.

## Invariants summary

Enumerated in full in `AGENTS.md`. The short list:

1. No `chain-commons`. Worker portability across payment systems is non-negotiable.
2. No cgo. FFmpeg is a subprocess, never a binding.
3. Providers boundary is a single hop; `os/exec`, RTMP, metrics, and gRPC clients live behind providers only.
4. No secrets in logs (stream keys redacted to 6-char prefix).
5. No customer concepts.
6. Test coverage ≥ 75% per package.
7. No code without a plan.
