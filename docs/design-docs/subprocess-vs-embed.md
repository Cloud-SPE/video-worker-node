---
title: Subprocess vs embed (the cgo decision)
status: accepted
last-reviewed: 2026-04-26
---

# Subprocess vs embed

Why the worker invokes FFmpeg as a subprocess instead of linking it via cgo / `lpms` / `libav*`.

## The decision

`transcode-worker-node` invokes FFmpeg as an `os/exec` subprocess. Zero cgo. Hard-enforced by `lint/no-cgo/`.

## Trade-offs considered

| Aspect | Subprocess | cgo embed (`lpms`-style) |
|---|---|---|
| Crash isolation | A bad input crashes ffmpeg; the daemon survives. | A bad input crashes the daemon. |
| Build hygiene per-vendor | One Docker target per vendor; binary is platform-independent. | One Go binary per vendor with vendor-specific cgo flags. |
| Resource accounting | `prlimit` per subprocess (CPU, RSS, FDs). | Process-wide limits only. |
| Cancellation | SIGTERM → SIGKILL grace per-job. | Implicit via shared address space. |
| Debugging | `-progress pipe:2` stderr output is the operator's first tool. | Internal logs only. |
| Performance | ~5% IPC overhead via stderr parsing. | Direct in-process call. |
| Portability | Pure Go binary; copy-deploy. | Vendor SDK + dynamic linker drama. |

The portability + crash-isolation trade is what motivated this rewrite. We are deliberately leaving 5% performance on the table to never deal with the `lpms` build matrix again.

## What this implies

- `internal/providers/ffmpeg/SystemRunner` is the only contact point with the binary.
- The Go binary is **FFmpeg-version-agnostic**. The Docker image places vendor-built FFmpeg at `/usr/local/bin/ffmpeg` (overrideable via `--ffmpeg-bin`).
- Progress feedback comes from parsing `-progress pipe:2` key=value lines (`internal/providers/ffmpeg.ParseProgressStream`).
- Resource limits use `prlimit(2)` — pluggable per mode in a future plan.

## What this does not do

- Does not preclude future cgo for other concerns (it does — but not here).
- Does not preclude `Module.Serve`-style streaming workloads embedded via cgo in **other** projects. This worker is workload-agnostic from `chain-commons`'s perspective and the cgo ban is repo-internal.

## Enforcement

`lint/no-cgo/` walks every `.go` file in the module and rejects:

- `import "C"`
- `//go:build cgo` directives
- `// +build cgo` directives

CI fails the build on violations. The lint has unit tests covering each rejection path.
