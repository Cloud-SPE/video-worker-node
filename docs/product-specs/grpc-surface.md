---
title: Worker operator gRPC surface
status: drafted
last-reviewed: 2026-04-26
---

# Operator gRPC surface

Local-only gRPC service over a unix socket (`--grpc-socket=/var/run/...`).
Filesystem permissions on the socket are the access-control mechanism.

## Methods

| RPC | Purpose |
|---|---|
| `Health()` | mode, dev flag, GPU profile, active job + stream counts |
| `ListJobs(filter)` | filter by status / mode / time range |
| `GetJob(job_id)` | full job record |
| `GetCapacity()` | concurrent encode slots, in-use, queued, active live streams |
| `ForceCancelJob(job_id)` | operator-initiated cancellation |
| `ListPresets()` | enumerate the loaded preset catalog |
| `ReloadPresets()` | pick up presets file changes without restart |

## Stability

These RPCs are exposed via a Go-native interface (in `internal/runtime/grpc/`)
that an external `.proto` file can wrap in a future plan. The surface is
operator-only — no customer ever sees this.

## Cross-references

- [`http-api.md`](http-api.md) — public HTTP surface
- [`../../DESIGN.md`](../../DESIGN.md) §"Trust boundaries"
