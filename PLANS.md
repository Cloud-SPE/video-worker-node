# PLANS — how work is planned in this repo

Plans are first-class artifacts. They are versioned in-repo alongside code so agents can read progress and decision history from the repository itself.

This repo follows the same planning convention as its sibling repos (the daemons in [`Cloud-SPE/livepeer-modules`](https://github.com/Cloud-SPE/livepeer-modules), and `openai-worker-node`). Sections below are intentionally mirror-copies of `openai-worker-node/PLANS.md`; differences are called out explicitly.

## Two kinds of plans

### Ephemeral plans

For small, self-contained changes (< ~50 LOC, single domain, no schema/protocol/config change). Written inline in the PR description. No file created.

### Exec-plans

For complex work: multi-domain, new ingest provider, schema/protocol change, GPU-vendor work, or anything an agent might pause mid-implementation and resume on later. Lives in `docs/exec-plans/active/`.

## Exec-plan file layout

```
docs/exec-plans/active/
├── 0001-<slug>.md      # in-flight
├── 0002-<slug>.md      # in-flight
docs/exec-plans/completed/
├── 0001-<slug>.md      # archived on merge
docs/exec-plans/tech-debt-tracker.md
```

IDs are monotonic, zero-padded to 4 digits, unique within this repo. Cross-repo coordination (e.g. this repo's `0007` depending on `payment-daemon`'s `0018`) is captured in the `## Cross-repo dependencies` section of a plan.

## Two templates

Most plans use the **lean** template. Multi-phase migrations / extractions / lifts use the **phased** template (current example: [`docs/exec-plans/active/0001-extract-from-platform.md`](docs/exec-plans/active/0001-extract-from-platform.md)).

### Lean template (default)

```markdown
---
id: 0042
slug: short-kebab-case
title: One-line title
status: active          # active | blocked | completed | abandoned
owner: <agent-or-human>
opened: YYYY-MM-DD
---

## Goal
One paragraph. What are we trying to achieve and why.

## Non-goals
What is explicitly NOT in this plan.

## Cross-repo dependencies
Plans in sibling repos this one needs completed first (or in-flight with
guaranteed order). Omit when none.

## Approach
- [ ] Step 1
- [ ] Step 2
- [ ] Step 3

## Decisions log
Append-only. Each decision: date + one-paragraph rationale.

### YYYY-MM-DD — <short title>
Reason: …

## Open questions
Things we need to answer before or during implementation.

## Artifacts produced
Links to PRs, generated docs, schemas created.
```

### Phased template (for migrations / lifts)

Phased plans add a `## Phases` section in place of `## Approach`, where each phase has its own deliverables + acceptance criteria, executed depth-first. They also carry a `## Progress log` separate from the decisions log. Use this template only when the work decomposes into ≥3 sequential phases each large enough to be its own day's work.

## Lifecycle

1. **Drafted** — file created in `active/` with `status: drafted`, awaiting steer on open decisions.
2. **Accepted** — open decisions resolved; status flips to `accepted` (phased plans) or `active` (lean plans). Execution begins.
3. **In progress** — steps checked off, decisions appended.
4. **Blocked** — status flipped to `blocked`, open-questions populated, escalated.
5. **Completed** — all steps checked / phases met; file moved from `active/` → `completed/`, status updated, final artifacts linked.
6. **Abandoned** — status flipped to `abandoned`, reason added to decisions log, file moved to `completed/`.

## When to open a plan

- Adding a new ingest provider (SRT, WHIP/WebRTC) — touches `internal/providers/ingest/`.
- Adding a new GPU vendor or revising preset shape — touches `internal/types/`, `presets/`, `Dockerfile`.
- Refactoring the FFmpeg subprocess wrapper — touches `internal/providers/ffmpeg/`.
- Replacing the BoltDB store with something else — touches `internal/providers/store/`.
- Changing the worker → shell webhook signature scheme — touches `docs/conventions/webhook-signing.md` + `providers/webhooks/`.
- Anything taking more than ~half a day of focused work, or anything that mutates the cross-process contract.

## Rules

- Never modify plans in `completed/`. History is immutable.
- Every PR that changes `internal/` must link to an exec-plan in its description (unless the change is ephemeral).
- Plans may reference design-docs; design-docs may not reference plans.
- `tech-debt-tracker.md` is append-only with strike-through when resolved.
- A plan in this repo that depends on a plan in a sibling repo must name the sibling plan in its `## Cross-repo dependencies` section.
