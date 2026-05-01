---
id: 0002
slug: v3-archetype-a-alignment
title: v3.0.0 — strip self-publishing, add `/registry/offerings`, rename `models` → `offerings`
status: abandoned
owner: agent
opened: 2026-04-29
depends-on: 0001
related:
  - https://github.com/Cloud-SPE/livepeer-network-suite/blob/main/docs/exec-plans/active/0003-archetype-a-deploy-unblock.md (master cross-repo plan, §D.2)
  - https://github.com/Cloud-SPE/livepeer-modules/blob/main/service-registry-daemon/docs/exec-plans/active/0004-v3-schema-and-archetype-a.md (modules schema bump that this regenerates against)
---

## Goal

Land this worker's v3.0.0 cut. Three pieces, stacked on top of plan
[`0001-extract-from-platform.md`](./0001-extract-from-platform.md)'s
code lift (Phase 2):

1. **Rip out worker self-publishing.** Workers do not dial publisher
   daemons under archetype A (network-suite plan 0003 §Decision 1).
   Delete `internal/service/capabilityreporter/` and
   `internal/providers/registryclient/` from whatever lifts in
   plan 0001 Phase 2; drop the `--registry-socket` /
   `--registry-refresh` CLI flags and the `registry_socket:` /
   `registry_refresh:` / `node_id:` / `public_url:` /
   `operator_address:` / `price_wei_per_unit:` config fields.

2. **Implement `/registry/offerings`.** New uniform endpoint emitting
   the modules-canonical capability fragment. Optional
   `OFFERINGS_AUTH_TOKEN` env for bearer auth. The orch-coordinator
   scrapes this on add-worker and on operator refresh
   (network-suite plan 0003 §Decision 5).

3. **Rename `models[]` → `offerings[]`** in the worker's `worker.yaml`
   parser to match the modules v3.0.0 schema. Workload-native
   `/capabilities` shape stays unchanged — the rename only applies
   inside the v3.0.0-canonical body of `/registry/offerings`.

## Closure note

Superseded by the finalized v3.0.1 contract and the newer local plans
[`0003-v3-0-1-worker-contract-alignment.md`](../active/0003-v3-0-1-worker-contract-alignment.md)
and [`0004-live-mode-session-failure-contract.md`](../active/0004-live-mode-session-failure-contract.md).
This plan was drafted against an earlier v3.0.0 understanding and is no
longer the right execution artifact.

## Sequencing relative to plan 0001

Plan 0001 lifts code from `livepeer-video-platform/apps/transcode-worker-node/`
into this repo. The lifted code already contains the
`capabilityreporter` and `registryclient` packages. Two clean orderings:

- **Inline:** during 0001 Phase 2 (code lift), drop the
  `capabilityreporter` and `registryclient` directories on the way in,
  add `/registry/offerings` in the same lift commit. One PR. Saves a
  round of churn.
- **Sequential:** finish 0001 Phase 2 with code lifted as-is; close
  0001; then this plan strips and rewires. Two PRs. Cleaner phase
  boundaries.

Pick at implementation time based on lift PR size. Default: inline,
because deleting code that immediately gets deleted again wastes
review attention.

## Non-goals

- Workload-native `/capabilities` response shape redesign — that's
  in plan 0001's Phase 2 lift scope (or follow-up local plans).
  This plan does NOT dictate what `/capabilities` looks like.
- Manifest signing, on-chain writes, or any registry advertisement
  beyond exposing `/registry/offerings` for the coordinator to scrape.

## Approach

### §A — Strip self-publishing

- [ ] Delete `internal/service/capabilityreporter/` (or never lift it
      in 0001 Phase 2 — see Sequencing above).
- [ ] Delete `internal/providers/registryclient/` (same).
- [ ] Drop CLI flags `--registry-socket`, `--registry-refresh` in
      `cmd/livepeer-video-worker-node/run.go` (and rename the binary
      to `video-worker-node` per 0001 Phase 2 if not already done).
- [ ] Drop `worker.yaml` config fields `registry_socket:`,
      `registry_refresh:`, `node_id:`, `public_url:`,
      `operator_address:`, `price_wei_per_unit:`. Unknown-field error
      at parse time is sufficient — no special-case "deprecated"
      branch.

### §B — Implement `/registry/offerings`

- [ ] New HTTP route `/registry/offerings` mounted on the worker's
      existing public listener (alongside `/capabilities` —
      operator's reverse proxy fronts it).
- [ ] Body shape: modules-canonical capability fragment.
      ```json
      {
        "capabilities": [
          {
            "name": "<canonical capability string>",
            "work_unit": "<frame|second|...>",
            "offerings": [
              { "id": "<preset-or-tier>", "price_per_work_unit_wei": "<decimal-string>" }
            ],
            "extra": { /* opaque, optional, may carry codec list etc. */ }
          }
        ]
      }
      ```
      Worker does not include its own `id`/`url`/`region`/`lat`/`lon`
      — those are operator-chosen and live in the coordinator's
      roster row.
- [ ] Optional bearer auth via new `OFFERINGS_AUTH_TOKEN` env. If
      set, the endpoint requires `Authorization: Bearer <token>`;
      otherwise plain HTTP. Default off — data ends up in the public
      manifest anyway, but the env hook lets operators add a barrier
      when they want one.
- [ ] Tests: handler unit-test the body shape and the auth gate.

### §C — `models[]` → `offerings[]` in `worker.yaml` parser

- [ ] Rename `models:` → `offerings:` and `model:` → `offering:` in
      `worker.yaml.example` and the worker config parser (wherever
      it lives post-0001 Phase 2).
- [ ] Tests assert that the old `models:` key is now an unknown
      field and produces a parse error.

### §D — Docs

- [ ] `DESIGN.md` and `README.md` — archetype-A framing: "worker is
      registry-invisible; orch-coordinator scrapes `/registry/offerings`
      and the operator confirms before publishing." Drop any sibling
      reference to `service_registry_publisher` or self-signing.
- [ ] `CHANGELOG.md` under v3.0.0 — initial release; stripped
      registry-publisher integration; introduced
      `/registry/offerings`; renamed `models` → `offerings` in
      worker.yaml.
- [ ] Strike-through tech-debt entries about registry integration
      (if any survived from the platform).

### §E — Tag

- [ ] Bump versions if there's a Go module-version mechanism in
      play; otherwise just tag.
- [ ] Tag `v3.0.0`.

## Decisions log

### 2026-05-01 — Abandoned in favor of the finalized v3.0.1 plan set
Reason: the latest network spec and worker-specific CR/spec files now
pin additional behavior this draft does not cover: shared `worker.yaml`
as the canonical worker/receiver contract, top-level `worker_eth_address`
and `auth_token`, `/capabilities` deletion, `/health` replacing
`/healthz`, standardized video capability strings, and the updated
payment-daemon proto surface. Keeping this draft active would advertise
the wrong migration path.

## Open questions

- **Modules-project version tag** — CONFIRMED `v3.0.0` (operator
  answered 2026-04-29).
- **Manifest `schema_version` integer** — CONFIRMED `3`; this worker
  doesn't directly emit the schema_version (only the publisher does)
  but `/registry/offerings` body shape MUST match what modules v3
  expects in `nodes[].capabilities[]`.
- **Daemon image pinning** — CONFIRMED hardcoded `v3.0.0` (every
  component lands at v3.0.0 in this wave; no tech-debt entry needed).
- **Sequencing inline vs sequential** (see above) — operator's call at
  PR time. Default: inline.
- Does `/registry/offerings` share the HTTP mux with the workload
  routes (`/v1/video/*`, `/stream/*`) or sit on the metrics listener?
  Default: same listener as `/capabilities` (`:8081`).

## Acceptance gates

- All §A–§E checkboxes ticked.
- `make test` green.
- `make doc-lint` green.
- A coordinator can hit `/registry/offerings`, parse the body, and
  pre-fill an operator's roster row with the returned offerings
  (verified as part of network-suite plan 0003 acceptance criterion
  #13).
- `v3.0.0` tag pushed.

## Artifacts produced

To be filled at plan completion. Expected: one PR (if inline with
0001 Phase 2) or two PRs (if sequential).

## Follow-ups

- Workload-native `/capabilities` response shape is plan 0001's
  Phase 2 design call; this plan inherits whatever lands there.
- HSM/KMS-backed signing is a modules-project concern, not a worker
  concern; not in scope here.
