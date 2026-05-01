---
title: Worker discovery
status: drafted
last-reviewed: 2026-04-26
---

# Worker discovery

How the shell finds workers that can handle a given capability.

> **Status**: drafted. Worker side is the `GET /registry/offerings`
> HTTP surface plus shared `worker.yaml` capability catalog. Resolver-
> side implementation is the consuming shell/coordinator concern.

## The model

Workers expose capability fragments on `GET /registry/offerings`.
Orch-coordinator performs one-shot scrape of known workers, stores the
results in draft state, and the operator confirms the resulting roster
before signing/publication. The consuming shell resolves against that
operator-confirmed roster rather than asking the worker directly.

## Capability strings

Capability advertisement uses string identifiers. The video platform's
canonical capability set:

| Capability | Status | Meaning |
|---|---|---|
| `video:transcode.vod` | MVP | Worker can do VOD transcoding |
| `video:transcode.abr` | MVP | Worker can do ABR-ladder transcoding |
| `video:live.rtmp` | MVP | Worker can ingest RTMP and produce live HLS |
| `video:live.srt` | backlog | Worker can ingest SRT |
| `video:realtime.whip` | backlog (WebRTC) | Worker (or paired SFU) can accept WHIP |

A worker's `worker.yaml` declares which it advertises; mismatches
between declared capability and actual support should fail startup
validation or dispatch-time checks.

## `WorkerResolver` adapter

The engine's `WorkerResolver` interface ([`adapter-contracts.md`](adapter-contracts.md))
returns a list of worker candidates. The shell's impl typically wraps
the coordinator/operator roster with:

- Local cache (5s stale-while-revalidate) to avoid hammering the daemon on
  high-throughput dispatch.
- Filtering by GPU vendor when the engine indicates a vendor preference
  (relevant for live encode at MVP — NVIDIA-only).
- Dead-worker pruning: workers that recently failed dispatches are
  temporarily de-prioritized (circuit-breaker pattern).

## Worker selection (sticky-on-asset)

For VOD, all renditions of one asset go to the same worker (hash on
asset.id mod number of available workers). Source download cost amortizes;
cache locality wins.

For Live, the worker is bound at the time the broadcaster's RTMP connection
is accepted. The shell records `media.live_streams.worker_url` so subsequent
worker → shell callbacks (validate-key happened on this worker; tick comes
from this worker; recording-finalize from this worker) are routed
consistently.

Smarter scheduling (queue-depth-aware, GPU-availability-aware,
failure-domain-spreading) is post-MVP.

## Bootstrapping a new worker

```
1. Operator deploys worker host with `worker.yaml` advertising capabilities.
2. Worker starts payment-daemon (receiver mode) co-located.
3. Worker serves `GET /registry/offerings`.
4. Orch-coordinator scrapes the worker and stores the draft capability fragment.
5. Operator confirms the roster and signs/publishes through secure-orch tooling.
6. Shell resolver picks up the updated roster on the next refresh.
```

## Capability mismatch handling

If the shell tries to dispatch `video:live.rtmp` and the resolver returns
zero workers (e.g., all NVIDIA workers down), the dispatcher returns
`NoWorkersAvailable` and the asset/stream transitions to `errored` with
a structured error.

Tracked as tech-debt: queueing dispatches awaiting worker availability for
a configurable window, vs failing immediately. MVP fails immediately.

## Cross-references

- [`payment-integration.md`](payment-integration.md) — how dispatch works
  once a worker is selected
- [`adapter-contracts.md`](adapter-contracts.md) §`WorkerResolver`
- `livepeer-network-spec-v3.md` / `video-worker-node-spec-v3.md` — the
  v3 worker discovery and roster flow this doc describes
