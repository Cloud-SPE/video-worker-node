---
title: Tech debt tracker
status: drafted
last-reviewed: 2026-04-29
---

# Tech debt tracker

**Append-only.** Resolved entries get strike-through, not deletion. The file is the historical record of known shortcuts, deferrals, and "we'll fix this later" calls.

## Format

```
## YYYY-MM-DD — <one-line summary>
**Severity:** low | medium | high
**Owner:** <agent-or-human>
**Why it's debt:** one paragraph.
**What it would take to resolve:** one paragraph.
**Linked plans / issues:** if any.
```

Strike-through resolved entries with `~~...~~` plus a `**Resolved YYYY-MM-DD:**` line.

## Entries

Chronological order (oldest first). Append at bottom.

## 2026-04-26 — SRT ingest provider

**Severity:** low
**Owner:** unassigned
**Why it's debt:** `providers/ingest/` is wire-protocol-agnostic; only the RTMP impl ships at MVP. SRT is the industry-standard alternative for low-latency live ingest. Lifted from the monorepo's worker-internal tracker.
**What it would take to resolve:** add a new `providers/ingest/srt/` impl using a pure-Go SRT library, register it via the existing IngestProvider interface, add config + capability advertisement.
**Linked plans / issues:** none yet — open a plan when first customer asks.

## 2026-04-26 — WHIP/WebRTC ingest provider

**Severity:** low
**Owner:** unassigned
**Why it's debt:** WebRTC realtime ingest path. Adds a `providers/ingest/whip/` impl plus a WHEP playback bridge. Significant architectural addition — was tracked monorepo-level because it touches the shell + a new SFU integration; now lives here as the video-worker-node's own debt entry. Lifted from the monorepo's worker-internal tracker.
**What it would take to resolve:** new ingest provider + SFU integration + cross-component design-doc + a consuming-shell plan in whichever shell adopts it.
**Linked plans / issues:** none yet.

## 2026-04-26 — Intel / AMD live encode verification

**Severity:** low
**Owner:** unassigned
**Why it's debt:** Live encode is NVIDIA-only at MVP. The Trickle blocker that originally forced this is removed; verifying QSV (Intel) + VAAPI (AMD) live works under the RTMP-based path is a follow-up. Lifted from the monorepo's worker-internal tracker.
**What it would take to resolve:** a hardware-bench verification pass on Intel + AMD hosts, plus a small plan to flip the per-vendor `live_supported` capability flag and document any flag changes.
**Linked plans / issues:** none yet — verification + plan once a customer needs vendor choice for live.

## 2026-04-26 — Lint stub fixture tests (`lint/{layer-check,doc-gardener}`)

**Severity:** low
**Owner:** unassigned
**Why it's debt:** `lint/layer-check` and `lint/doc-gardener` are stubs that delegate enforcement to `golangci-lint`'s depguard / external doc-gardening. Fixture-based tests would let us assert their behavior changes when rules accumulate. Coverage exempted at MVP. Lifted from the monorepo's worker-internal tracker.
**What it would take to resolve:** add fixture inputs + golden-file assertions; remove the coverage exemptions for these packages.
**Linked plans / issues:** none — addressed once one of those lints grows real rules.

## 2026-04-29 — Data race in `internal/runtime/metrics` test harness

**Severity:** medium
**Owner:** unassigned
**Why it's debt:** `go test -race ./internal/runtime/metrics/...` fails with a data race on `Server.Listen()` (read at `metrics_test.go:63`, write at `metrics.go:70`). The Listen method is launched in a goroutine by the test and the test then reads state from it without synchronization. **This is pre-existing in the source repo (`livepeer-video-platform/apps/transcode-worker-node`)**; the Phase 2 lift faithfully reproduced it. Plan 0001's Phase 2 non-goal (relocation, not rewrite) means fixing it here is out of scope.
**What it would take to resolve:** add a mutex or channel-based readiness signal to `Server.Listen` so test code can wait for the listener to be bound before reading; alternatively, re-architect the Server to have a synchronous bind step + async serve step. ~1 hour focused work.
**Linked plans / issues:** [`0001-extract-from-platform.md`](active/0001-extract-from-platform.md) Phase 2 progress log.

## 2026-04-29 — `make proto` fails on a stale buf.yaml reference

**Severity:** low
**Owner:** unassigned
**Why it's debt:** `proto/buf.yaml` declares a module `livepeer/transcode/v1` with no `.proto` files in that directory. `make proto` (when buf is installed) fails immediately with `Module "path: "livepeer/transcode/v1"" had no .proto files`. The Makefile target gracefully degrades when buf is absent ("buf not installed; skipping"), so day-to-day work isn't blocked — the vendored stubs under `proto/clients/livepeer/{payments,registry}/v1/` continue to work. **Pre-existing in the source repo**; the Phase 2 lift faithfully reproduced it.
**What it would take to resolve:** either delete the `livepeer/transcode/v1` module entry from `buf.yaml` (if no proto is planned there), or create the directory + add a `.proto` file (if a worker-owned proto surface is planned). ~10 minutes once a direction is chosen.
**Linked plans / issues:** [`0001-extract-from-platform.md`](active/0001-extract-from-platform.md) Phase 4.

## 2026-04-29 — `doc-gardener` lint is a no-op stub

**Severity:** medium
**Owner:** unassigned
**Why it's debt:** `lint/doc-gardener/run.go` has `Run()` as a no-op (its package doc explicitly says so). The `Makefile` has no `doc-lint` target invoking it. As a result, this repo has zero **automated** enforcement of: (a) cross-doc link integrity, (b) frontmatter `last-reviewed` freshness, (c) detection of dead intra-repo links, (d) detection of links escaping the repo. The harness PDF treats doc-gardening as load-bearing — without it, the docs system silently rots.
**What it would take to resolve:** flesh out `lint/doc-gardener/Run()`: walk every `*.md` under `docs/` (and pillar docs at root), parse markdown link targets, check each resolves to an existing file (or matches an explicit allowlist of external URLs), check frontmatter has `title` + `status` + `last-reviewed` ≤ 90 days old, exit non-zero on any finding. Add a `doc-lint` Makefile target invoking it. Add it to CI when CI lands.
**Linked plans / issues:** [`0001-extract-from-platform.md`](active/0001-extract-from-platform.md) Phase 3.

## 2026-04-29 — Repo license unresolved (D6)

**Severity:** medium
**Owner:** mazup
**Why it's debt:** the repo is intended public ([D2](active/0001-extract-from-platform.md)) but ships with no `LICENSE` file. Default US copyright applies (all rights reserved); adopters can read but cannot legally use, fork, or modify the code. Mirrors the platform monorepo's TBD status for the worker.
**What it would take to resolve:** decide between MIT (matches `@cloudspe/video-core`), Apache-2.0, a proprietary EULA, or formal indefinite deferral. Add the chosen `LICENSE` file and update the repo's GitHub "license" field.
**Linked plans / issues:** [`0001-extract-from-platform.md`](active/0001-extract-from-platform.md) decision **D6**.
