---
title: References index
status: drafted
last-reviewed: 2026-04-29
---

# References

External material brought into the repo so it is **legible to agents**. Per the harness PDF: anything not in the repo is invisible to the agent. References are how we make Slack threads, blog posts, design notes, and PDFs visible.

## Catalog

- [`openai-harness.pdf`](openai-harness.pdf) — OpenAI's "Harness engineering: leveraging Codex in an agent-first world" (2026-02-11). The methodology this repo is scaffolded against. Read this before contributing.

## Phase 3 additions

[Exec-plan 0001](../exec-plans/active/0001-extract-from-platform.md) Phase 3 lifts:

- `lifted-from-source.md` — provenance for the proto stubs vendored under `proto/livepeer/` (sourced from `livepeer-modules`, regenerated via `make proto`). Unchanged from the worker's existing module-internal copy.

No `lifted-from-monorepo.md` manifest is produced — see plan 0001 decision **D3** ("no source provenance from `livepeer-video-platform`").

## Conventions

- A reference doc is read-only context. **Do not** quote or summarize a reference inline in code or other docs without checking the date — references can age out.
- Each reference carries `last-reviewed` in frontmatter so the doc-gardener lint can flag stale citations.
- External links in references decay; prefer in-repo PDF or markdown copies over hyperlinks where the source license permits.
