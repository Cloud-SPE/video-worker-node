---
id: 0003
slug: v3-0-1-worker-contract-alignment
title: v3.0.1 worker contract alignment
status: drafted
owner: agent
opened: 2026-05-01
depends-on: 0001
---

## Goal
Align `video-worker-node` with the finalized v3.0.1 worker contract now
captured in the latest network spec and worker-specific CR/spec set.
That means moving the repo from its current flag-driven, partially
lifted state to the shared `worker.yaml` + canonical `/registry/offerings`
+ `/health` model already used by `openai-worker-node`, while preserving
the video workload-specific paid routes and layered-architecture rules.

## Non-goals
- New paid workload routes or a redesign of the existing VOD / ABR /
  live request bodies.
- Manifest generation, coordinator scrape logic, or secure-orch signing.
- SRT / WHIP ingest implementation. Only reserve the canonical strings
  and config shape for future additions.
- Release tagging or image publication work.

## Cross-repo dependencies
- `livepeer-modules-project/payment-daemon` current v3.0.1 proto and
  receiver catalog behavior are the reference contract this repo must
  regenerate against.
- `openai-worker-node` is the implementation reference for the shared
  `worker.yaml` parser shape, `/registry/offerings`, `/health`, and
  auth behavior.

## Phases

### Phase A — Shared config + proto baseline

Deliverables:
- Introduce a shared `worker.yaml` parser in `internal/config/`,
  matching the v3.0.1 top-level shape:
  `protocol_version`, optional `worker_eth_address`, optional
  `auth_token`, opaque `payment_daemon`, `worker`, `capabilities`.
- Enforce `KnownFields(true)`, reject `service_registry_publisher`,
  validate canonical video capability strings, validate work units, and
  require per-offering `backend_url`.
- Refresh vendored payment-daemon proto/stubs from the current
  modules-project source so this repo speaks `offerings`, not `models`,
  and does not expect `ListCapabilitiesResponse.protocol_version`.

Acceptance criteria:
- `cmd/livepeer-video-worker-node` accepts `--config=...` and boots from
  shared YAML in dev mode.
- Old `models:` / `service_registry_publisher:` / ad hoc capability
  strings fail closed at parse time.
- Generated payment proto matches the current modules-project source.

### Phase B — Public unpaid HTTP surface

Deliverables:
- Replace `GET /healthz` with `GET /health` on the main worker HTTP mux.
- Add `CurrentAPIVersion` and surface `api_version` plus
  `protocol_version` on `/health`.
- Rework `GET /registry/offerings` to project directly from parsed
  config, strip `backend_url`, emit canonical capability strings, emit
  `constraints` / `extra` when present, and include top-level
  `worker_eth_address` when configured.
- Gate `/registry/offerings` with top-level `worker.yaml.auth_token`
  using constant-time bearer compare.
- Delete `GET /capabilities` from the main worker HTTP surface.

Acceptance criteria:
- `/health` and `/registry/offerings` match the v3.0.1 contract.
- `/capabilities` is gone from code, tests, and product-spec docs.
- Unit tests cover happy-path projection, bearer auth, omitted
  `worker_eth_address`, and `backend_url` stripping.

### Phase C — Runtime integration + startup consistency

Deliverables:
- Wire the parsed worker config into runtime startup instead of the
  current flag-only capability/auth model.
- Decide and implement the startup worker ↔ payment-daemon consistency
  check against the refreshed `ListCapabilities` RPC contract.
- Preserve mode-specific runtime wiring (`vod`, `abr`, `live`) while
  moving ingress/listen/auth/capability values under parsed config.

Acceptance criteria:
- Worker boot path uses shared YAML as the source of truth for the
  worker capability catalog and `/registry/offerings` output.
- Any worker/daemon catalog drift fails closed with a clear startup
  error.
- Dev-mode and non-dev startup tests cover the shared config path.

### Phase D — Deploy/docs/examples sweep

Deliverables:
- Update `worker.example.yaml`, compose files, operations docs,
  product-spec docs, `README.md`, `DESIGN.md`, `PRODUCT_SENSE.md`, and
  `AGENTS.md` to the archetype-A and v3.0.1 contract.
- Remove worker-host `service-registry-daemon` deployment assumptions.
- Sweep stale `/healthz`, `/capabilities`, `BYOC`, `bridge protocol`,
  `OFFERINGS_AUTH_TOKEN`, `registry-socket`, and publisher-mode wording.
- Encode canonical video capability strings in examples and fixtures:
  `video:transcode.vod`, `video:transcode.abr`, `video:live.rtmp`.

Acceptance criteria:
- Compose/docs no longer instruct operators to run a publisher daemon on
  the worker host.
- Examples and fixtures use the canonical v3.0.1 video capability
  strings.
- `make test` and `make doc-lint` pass.

## Decisions log

### 2026-05-01 — Canonical video capability strings are standardized
Reason: the latest network spec now pins explicit standardized strings
for video workloads rather than a single umbrella capability or ad hoc
repo-local naming. This plan treats `video:transcode.vod`,
`video:transcode.abr`, and `video:live.rtmp` as the v3.0.1 source of
truth.

### 2026-05-01 — Shared worker.yaml follows openai-worker-node shape
Reason: the latest worker-specific spec explicitly calls out
`openai-worker-node` as the reference for top-level `worker_eth_address`,
`auth_token`, opaque `payment_daemon`, and strict parse behavior. Reusing
that shape keeps the sibling workers consistent and reduces cross-repo
drift.

## Open questions
- Exact worker ↔ payment-daemon startup consistency scope after the
  proto refresh: capability string + work unit + offering id + price are
  clearly comparable; `constraints` / `extra` are worker-only and likely
  should not be part of the daemon comparison.
- Whether `/health` should include additional operator-triage fields
  beyond the spec minimum (`mode`, version, uptime, inflight). The spec
  permits extensions, but the shape should stay stable once chosen.

## Artifacts produced
- None yet.
