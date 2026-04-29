# PRODUCT_SENSE — video-worker-node

## What we're building

Infrastructure. The payee-side video adapter that turns a host running FFmpeg-capable GPUs plus a `livepeer-payment-daemon` into a sellable "video worker" on the Livepeer BYOC network. Not a product for end users; a product for **operators** who already run GPU hardware and want to monetize it through any video gateway (shell) that speaks the bridge protocol.

This worker is **the sister of `openai-worker-node`** — same scaffolding pattern, same payment architecture, different workload (FFmpeg subprocess transcoding instead of OpenAI-compatible inference). What the inference worker does for tokens, this worker does for video seconds.

## Who uses this

Three personas.

### The worker operator (primary)

Someone with GPU capacity who wants to sell **transcode-seconds** (VOD, ABR ladders, live HLS) for Livepeer micropayments. They care about:

- **Deployment simplicity:** one config file (`worker.yaml`), one binary per GPU vendor, stands up in an afternoon via `docker compose up`.
- **Income correctness:** every paid request is actually paid; the ledger never credits something it didn't receive. Restarts don't double-bill or lose partial encodes.
- **Crash isolation:** an FFmpeg segfault on a malformed input does not take down the worker process. FFmpeg is a subprocess, not a library.
- **Multi-vendor builds:** one source tree, three Docker tags (`-nvidia`, `-intel`, `-amd`). They pick the build that matches their hardware.
- **Observability:** which encode is running, current FPS, GPU utilization, queue depth, ticket validation rate — all visible without ssh.
- **Per-preset pricing they can tune** without code changes.

### The shell operator (secondary)

Someone running a video gateway (Cloud-SPE's `livepeer-video-gateway`, or any other HTTP API that fronts these workers) who lists this worker via `service-registry-daemon`. They care about:

- A **predictable HTTP contract** (`/health`, `/capabilities`, `POST /v1/video/transcode`, `POST /stream/start`, RTMP `:1935`).
- A **truthful `/capabilities` response** — what the worker advertises (resolutions, codecs, GPU vendor, live support) is what the worker can deliver.
- **Clean error modes:** `payment-required`, `capacity-exceeded`, `model-unsupported`, `gpu-busy` are distinguishable.
- **Reliable webhooks** (HMAC-signed, retried with backoff) so state transitions in the worker can be reflected in the shell's DB without polling.

### The end-customer developer (indirect)

End customers never see this worker — they talk to the shell. But they care (indirectly) about:

- **Transcode latency** dominated by encode time, not orchestration overhead.
- **Live HLS time-to-first-segment** under ~10s from RTMP first-frame.
- **No corruption** of customer-specified output presets — the worker MUST honor what the shell asked for.

## What "good" looks like

- An operator reads `DESIGN.md`, writes a `worker.yaml`, runs `docker compose up`, and starts serving paid VOD jobs within an hour.
- A VOD job submitted via `POST /v1/video/transcode` with a valid payment ticket completes within the source duration's expected encode time on the configured GPU vendor.
- An ABR job produces a master HLS manifest plus one playlist per rendition, all uploaded to the operator-configured object storage URL.
- A live broadcaster connecting to `rtmp://host:1935/live/{stream_key}` produces HLS segments visible at the storage URL within ~10s of first frame.
- The worker advertises its capabilities to `service-registry-daemon` on startup and refreshes per the configured interval; the shell sees them within 5s of refresh.
- The worker's metrics endpoint surfaces every encode's status, FPS, and GPU vendor — operators can spot a struggling worker without ssh.
- A worker restart resumes any in-flight VOD/ABR jobs from BoltDB without re-debiting a customer or losing a partial encode.
- Replacing `paymentclient` with a different gRPC client is the **only** change needed to talk to a different paid-work scheme. Worker portability is enforced by lint, not by vigilance.

## Anti-goals (workload-only invariants)

- **No `chain-commons` import.** Worker portability across payment systems is non-negotiable. Hard-fail lint enforces.
- **No cgo, no FFmpeg bindings.** FFmpeg is a subprocess. Crash isolation, multi-vendor build hygiene, no `lpms`-style binding model. Hard-fail lint enforces.
- **No customer concepts.** No knowledge of API keys, projects, billing, customer-facing webhooks. Webhooks here are operator-configured worker → shell internal callbacks (e.g., to `/internal/live/*` paths); customer-facing webhooks are the shell's job.
- **No multi-tenancy.** Each worker is a single-operator host. Different tenants run different workers.
- **No on-chain interaction.** Tickets in, debits out — `payment-daemon` does the chain talking.
- **No editing, no clipping, no transcoding-aware AI.** Encode in, manifest out. Anything richer belongs upstream.
- **No DRM.** Plaintext segments out; signed-token playback is the shell's job.
- **No queue.** If all GPU slots are busy, the worker returns `503`; it does not buffer requests.
- **No DB-of-record.** BoltDB is durable scratch space for in-flight jobs; the source-of-truth lives in the shell.

## Non-ambitions (worth naming)

- No admin UI. Observability is metrics + logs + the payment-daemon's own RPCs.
- No automatic backend discovery — but `service-registry-daemon` discovery is exactly the point.
- No hot reload. Config changes restart the worker.
- No multi-language polyglot. Worker is Go; that is the entire toolchain.

## Cross-component context

The reference shell that consumes this worker is Cloud-SPE's video gateway, but **nothing in this repo is shell-specific**. Any HTTP server that:

1. Resolves a worker via `service-registry-daemon`,
2. Sends a `livepeer-payment` ticket from a sender-mode `payment-daemon`,
3. POSTs an OpenAPI-compatible body (or starts an RTMP session via `/stream/start`),

…can drive this worker. The bridge-pattern is the public contract.

WebRTC realtime ingest (WHIP/WHEP + SFU) is a backlog item. SRT ingest is a backlog item. Both are clean additions under `internal/providers/ingest/` without touching the existing RTMP path.
