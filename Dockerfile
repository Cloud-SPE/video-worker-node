# syntax=docker/dockerfile:1.7
#
# Multi-stage Dockerfile for livepeer-video-worker-node.
#
# Targets:
#   --target=go-builder        — builds the pure-Go binary (no GPU)
#   --target=runtime-nvidia    — vendor-built FFmpeg (NVENC/CUDA) + binary
#   --target=runtime-intel     — vendor-built FFmpeg (QSV/oneVPL) + binary
#   --target=runtime-amd       — vendor-built FFmpeg (VAAPI) + binary
#
# The binary itself is FFmpeg-version-agnostic. Each runtime stage layers
# the appropriate vendor FFmpeg at /usr/local/bin/ffmpeg. The binary is
# CGO_ENABLED=0 — pure Go — so it runs in a distroless-ish base too.
#
# In a production build pipeline, the FFmpeg-* stages are typically
# pulled from pre-built `codecs-builder` and vendor-specific images so
# this Dockerfile stays cheap to rebuild on Go changes.
#
# Build context: this directory (apps/transcode-worker-node/).

ARG VERSION=dev

# --- go-builder ---
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG VERSION
RUN CGO_ENABLED=0 GOOS=linux go build \
        -trimpath \
        -tags 'netgo osusergo' \
        -ldflags "-s -w -X main.version=${VERSION}" \
        -o /out/livepeer-video-worker-node \
        ./cmd/livepeer-video-worker-node

# --- ffmpeg-nvidia stage (placeholder — real build pulls from codecs-builder) ---
FROM nvidia/cuda:12.4.1-runtime-ubuntu22.04 AS ffmpeg-nvidia
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/* && \
    cp /usr/bin/ffmpeg /usr/local/bin/ffmpeg

# --- ffmpeg-intel stage (placeholder) ---
FROM intel/oneapi-runtime:2024.2.1-0-devel-ubuntu22.04 AS ffmpeg-intel
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/* && \
    cp /usr/bin/ffmpeg /usr/local/bin/ffmpeg

# --- ffmpeg-amd stage (placeholder) ---
FROM rocm/pytorch:rocm7.2.2_ubuntu22.04_py3.10_pytorch_release_2.10.0 AS ffmpeg-amd
RUN apt-get update && apt-get install -y --no-install-recommends \
        ffmpeg \
    && rm -rf /var/lib/apt/lists/* && \
    cp /usr/bin/ffmpeg /usr/local/bin/ffmpeg

# --- runtime-nvidia ---
FROM ffmpeg-nvidia AS runtime-nvidia
COPY --from=go-builder /out/livepeer-video-worker-node /usr/local/bin/
RUN useradd -u 65532 -m livepeer
USER livepeer
ENTRYPOINT ["/usr/local/bin/livepeer-video-worker-node"]

# --- runtime-intel ---
FROM ffmpeg-intel AS runtime-intel
COPY --from=go-builder /out/livepeer-video-worker-node /usr/local/bin/
RUN useradd -u 65532 -m livepeer || true
USER livepeer
ENTRYPOINT ["/usr/local/bin/livepeer-video-worker-node"]

# --- runtime-amd ---
FROM ffmpeg-amd AS runtime-amd
COPY --from=go-builder /out/livepeer-video-worker-node /usr/local/bin/
USER 1000:1000
ENTRYPOINT ["/usr/local/bin/livepeer-video-worker-node"]
