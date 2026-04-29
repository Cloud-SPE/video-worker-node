---
title: GPU vendor strategy
status: accepted
last-reviewed: 2026-04-26
---

# GPU vendor strategy

The worker supports three GPU vendors at v1: NVIDIA, Intel, and AMD. The operator picks one vendor per host via `--gpu-vendor=auto|nvidia|intel|amd`. `auto` walks all three and picks the first one detected.

## Detection

Detection lives in `internal/providers/gpu/`. Subprocess shims:

- **NVIDIA**: `nvidia-smi --query-gpu=name,driver_version,memory.total --format=csv,noheader,nounits`. Parses model, driver, VRAM. Sets max sessions to `0` (unlimited) for data-center cards (A100/H100/L4/L40/Tesla); `8` for consumer cards.
- **Intel / AMD**: `vainfo` against `/dev/dri/renderD128`. Parses VAProfile lines for H.264 / HEVC / AV1 capability and a driver version line. Both vendors share the path; the configured `--gpu-vendor` distinguishes them.

A failed detection in a vendor-required configuration is a **fatal preflight error** (`PREFLIGHT_NO_GPU`). No CPU fallback.

## Codec selection

`internal/providers/ffmpeg.codecFlag` maps `(codec, vendor)` → ffmpeg encoder name:

| Codec | NVIDIA | Intel | AMD |
|---|---|---|---|
| h264 | `h264_nvenc` | `h264_qsv` | `h264_vaapi` |
| hevc | `hevc_nvenc` | `hevc_qsv` | `hevc_vaapi` |
| av1 | `av1_nvenc` (Ada+) | `av1_qsv` | `av1_vaapi` |

`hwaccel` flags differ per vendor (`-hwaccel cuda` / `-hwaccel qsv` / `-hwaccel vaapi -vaapi_device /dev/dri/renderD128`).

## Image-per-vendor

Each runtime image is a separate Docker target:

| Target | Base | FFmpeg build |
|---|---|---|
| `runtime-nvidia` | `nvidia/cuda:*-runtime` | `--enable-nvenc --enable-cuvid` |
| `runtime-intel` | `intel/oneapi-runtime:*` | `--enable-libvpl` (oneVPL/QSV) |
| `runtime-amd` | `rocm/pytorch:*-runtime` (or similar) | `--enable-vaapi` |

`build.sh` builds all three. Operators run the image matching their host GPU.

## Live-mode limitation

Live mode is **NVIDIA-only** at v1. Intel QSV and AMD VAAPI live encoding has known stability issues with Trickle protocol I/O — tracked in `tech-debt-tracker.md` for a future plan.

The lint does not enforce this — the `liverunner` accepts any GPU profile. The runtime check (and operator runbook) point at the limitation.
