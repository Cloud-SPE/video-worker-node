---
title: Recording bridge (live → VOD)
status: accepted
last-reviewed: 2026-04-26
---

# Recording bridge

When a live stream ends with `recording_enabled = true`, the platform
converts the live segments into a VOD asset. The same `PlaybackID` that
served live HLS during the stream then serves the recording — `dispatchPlaybackResolve`
detects the `recording_ready` status and rewrites the redirect target.
This doc captures **what gets copied vs. re-encoded**, **how the storage
keys transition**, and **what fires when**.

## Trigger sequence

1. Worker's per-stream goroutine exits (broadcaster disconnect or graceful close with `recording_enabled=true`).
2. Worker calls `POST /internal/live/session-ended` with `reason: 'graceful'`.
3. Shell flips `media.live_streams.status` → `recording_processing`, fires `video.live_stream.ended`.
4. Worker's `livecdn.Mirror` finishes its final scan, pushing any straggler segments to the sink.
5. Worker calls `POST /internal/live/recording-finalized` with the cumulative segment list + master key + total duration.
6. Shell's `recordingFinalizer.finalize()` runs (described below).
7. Shell flips `media.live_streams.status` → `recording_ready`, sets `recording_asset_id`, fires `video.live_stream.recording_ready` AND `video.asset.ready`.

## Adopt-as-is (the MVP choice)

Live segments are already H.264 / HLS / .ts files in the right shape for
VOD playback. Re-encoding them just to relocate the bytes is wasteful.
Instead the bridge does **server-side copy** (S3 `CopyObject` / MinIO
equivalent) from the live prefix to the asset prefix:

```
live/{stream_id}/h264/{rung}/segment_NNNNN.ts
                    │
                    ▼
assets/{asset_id}/hls/h264/{resolution}/segment_NNNNN.ts
```

Plus the variant playlists:

```
live/{stream_id}/h264/{rung}/playlist.m3u8
                    │
                    ▼
assets/{asset_id}/hls/h264/{resolution}/playlist.m3u8
```

The master manifest is **rebuilt from scratch** via the engine's
`buildMasterManifest`, since (a) the live master.m3u8 referenced live
URIs which won't be served from the asset prefix, and (b) tier upgrades
add HEVC/AV1 rungs the live master never knew about.

A `media.renditions` row is inserted per rung, so the recording asset
plays through the same `dispatchPlaybackResolve` path as upload-derived
VOD.

## Tier upgrade (post-MVP)

`encoding_tier ≥ standard` recordings can re-encode the highest live
rendition (1080p) into HEVC + AV1 ladders after the baseline adoption
finishes. This reuses the VOD job DAG from plan 0005: the bridge inserts
an `encode` job with the adopted 1080p variant playlist as input.

MVP ships baseline only; `encoding_tier` defaults to `baseline` for
live recordings. Operators can upgrade per-stream by writing the
defaultEncodingTier into RecordingBridgeDeps at composition time;
the field is wired through. Full per-stream tier control is tech-debt.

## Storage layout transition

| Phase | Live prefix | Asset prefix |
|---|---|---|
| `active` / `reconnecting` | `live/{stream_id}/h264/{rung}/segment_*.ts` and `playlist.m3u8` | (none) |
| `recording_processing` | live prefix still authoritative; bridge in flight | partially populated as copies land |
| `recording_ready` | live prefix retained for ~24h then TTL-deleted | full ladder lives here, master.m3u8 served from `assets/{asset_id}/hls/master.m3u8` |
| `ended` (no recording) | TTL-deleted after the configured live retention window | n/a |

`live/` TTL cleanup is operator-side (S3 lifecycle rule); the platform
relies on a 24h retention so any in-flight playback that hadn't yet
swapped over to the asset URL doesn't 404.

## Failure modes

- **Source segment missing during copy**: the worker's mirror has best-effort cleanup, so a few segments may have been deleted before finalize ran. The current bridge logs and skips; the asset still ships with the segments that did adopt successfully. Tech-debt: rebuild the variant playlist from the *adopted* segment list rather than copying the worker's playlist file (insulates against this).
- **Variant playlist already deleted**: caught + ignored. Master is rebuilt locally so the asset is still playable as long as segments survived.
- **Asset row already exists / collision**: ULID-keyed; collision impossible at MVP scale.
- **Worker crashes mid-finalize**: the stale-stream sweeper (90s default) eventually transitions the row to `errored`; operator can retry by manually advancing `live_streams.status` and replaying the worker callback (admin-tooling tech-debt).

## Why both events fire on success

`video.live_stream.recording_ready` is the live-stream-centric event
(includes `live_stream_id` and `playback_id`). `video.asset.ready` is
asset-centric (includes `asset_id` and `source_type='live_recording'`).
A customer's webhook subscriptions usually fall on one side or the
other; firing both means subscribers don't have to special-case live
recordings. Documented as part of plan 0006 §K.

## Cross-references

- [`live-state-machine.md`](live-state-machine.md)
- [`internal-callback-api.md`](internal-callback-api.md) — wire format for `/recording-finalized`.
- [`storage-layout.md`](storage-layout.md) — overall path conventions.
- Plan 0005 — VOD job DAG (reused for tier-upgrade re-encodes).
- Plan 0006 §G — original implementation plan.
