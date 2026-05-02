---
id: 0008
slug: rustfs-storage-alignment
title: Align worker docs and examples with RustFS as the storage baseline
status: completed
owner: agent
opened: 2026-05-02
depends-on: 0005
related:
  - https://github.com/rustfs/rustfs
  - /home/mazup/git-repos/livepeer-cloud-spe/livepeer-video-gateway/docs/exec-plans/active/0022-rustfs-object-storage-migration.md
---

## Goal

Replace MinIO-specific worker-repo references with RustFS or generic
S3-compatible wording, keeping the worker's actual transport contract
unchanged.

## Why this plan exists

The worker repo still references MinIO in design docs, conventions, and
historical acceptance text even though the user wants RustFS to be the
standard self-hosted object-store reference.

## Non-goals

- Rewriting the worker's storage transport layer.
- Adding a RustFS service to this repo's own compose unless a concrete
  worker-local runtime need appears.

## Approach

- [x] Sweep current docs for MinIO-specific wording and replace it with
      RustFS or generic S3-compatible terminology as appropriate.
- [x] Keep references to the gateway's playback/storage flow consistent
      with the gateway repo's RustFS migration.
- [x] Re-run the worker checks that are relevant to doc/example changes.

## Acceptance gates

- Active worker docs no longer present MinIO as the default self-hosted
  object-store reference.
- Worker docs remain coherent with the gateway repo's object-store
  terminology.
