---
name: to-wiki
description: Ingest an approved artifact (PRD, spec, plan) into the project's living wiki — a persistent, cross-linked knowledge base that stays in sync with what's actually true, not a changelog of what happened.
disable-model-invocation: true
---

# To Wiki

Maintain `docs/wiki/` as a **compiled, compounding artifact** — not a retrieval index rebuilt from scratch each time, and not a changelog. Every ingest updates the wiki so it reflects current truth; it never just appends a new page and calls it done.

Model: <https://gist.github.com/karpathy/442a6bf555914893e9891c11519de94f> — raw sources are immutable, the wiki is the one layer you own and maintain, a schema file governs how.

## The three layers

1. **Raw sources** (immutable — never edit these): `docs/prd/*.md`, `docs/spec/*.md`, `docs/planning/*.md`, ADRs, the ticket tracker itself. You read these; you never write to them.
2. **The wiki** (`docs/wiki/` — yours to maintain):
   - `docs/wiki/index.md` — catalog: every page, one-line summary, category.
   - `docs/wiki/log.md` — append-only. One entry per ingest: `## [<date>] ingest | <what triggered this>`, followed by what changed and why.
   - `docs/wiki/overview.md` — the single functional summary: what does this system do, for whom, why it exists. This is the page a CEO or a brand-new hire reads first. Keep it current, not historical — it describes the system AS IT NOW STANDS, not a history of how it got there.
   - `docs/wiki/concepts/*.md`, `docs/wiki/decisions/*.md` — deeper pages: domain concepts, architecture/approach decisions and their tradeoffs, one page per durable idea. Cross-link liberally (`[term](../concepts/term.md)`).
3. **The schema** (`docs/wiki/SCHEMA.md`) — the conventions governing the wiki itself: page types, naming, when something gets its own page vs. folded into an existing one, audience assumptions. If it doesn't exist yet, bootstrap it (template below) on your first ingest. It co-evolves with the wiki — update it if you find yourself inventing a new page type or convention, and say so in your PR.

## Audience (write for this on every page, not just overview.md)

The wiki serves **everyone touching the project — CEO to developers, new to old** — not just the next agent. Assume the reader has NOT read the PRD/spec/tickets. Explain what and why in plain language before any implementation detail. This is the whole point: comprehension debt builds up as agents build faster than humans can track, and this is the bridge back.

## Process (ingest)

1. **Read the raw source** for this trigger — the artifact named by the ticket (approved PRD, or approved spec + ticket breakdown + planning doc). Do not read it in isolation — also read `docs/wiki/index.md`, `docs/wiki/log.md`, and any existing page the new material touches or contradicts. An ingest is an UPDATE to the compounding artifact, not a blind new-page write.
2. **Bootstrap `docs/wiki/SCHEMA.md`** if this is the wiki's first ingest ever (template below).
3. **Update or create pages**:
   - PRD-approved trigger: write/refresh `overview.md` — what the system does, for whom, and why — from the user's/business perspective, not the technical one. Create/update concept pages for any domain term the PRD introduces.
   - Plan-approved trigger: write/refresh a decisions page (or `docs/wiki/decisions/<slug>-approach.md`) covering: the approach taken, the key decisions and their tradeoffs (not every decision — the ones a future reader would otherwise have to reconstruct by reading old tickets), and the sequencing rationale (why this order — walking skeleton first, tracer bullets for the real unknowns, what's deferred and why). Link it from `overview.md` and `index.md`.
   - Existing pages that the new material makes stale or contradicts get UPDATED in place, not left to rot alongside a newer, conflicting page.
4. **Update `docs/wiki/index.md`** with every new/changed page.
5. **Append one entry to `docs/wiki/log.md`** — what triggered this ingest (ticket ID), what changed, why.
6. **Publish**: branch off `main` as `wiki/<slug>`, commit, push, open a PR. Post the PR link on the tracking ticket and set the `Wiki PR` property (a property distinct from the phase's own `PR` property — that one's already holding the phase's own artifact PR by the time you run). Never merge it yourself — a human reviews and approves via the ticket's Wiki Approval property, same posture as a PRD/plan approval.

<schema-bootstrap-template>

# Wiki Schema

This file governs `docs/wiki/`'s own conventions — read it before every ingest, update it when a real new convention emerges (and say so in your PR, don't silently drift).

## Audience
Everyone on the project: non-technical stakeholders through senior engineers, new joiners through the team that's been here since day one. Plain language first; technical depth is additive, never a prerequisite for the top of a page.

## Page types
- `overview.md` — the one functional summary. Always current, never historical.
- `concepts/<slug>.md` — one durable domain idea per page.
- `decisions/<slug>.md` — one approach/architecture decision and its tradeoffs per page.
- `index.md` — catalog, kept in sync with every page that exists.
- `log.md` — append-only ingest history. Never edit past entries.

## When something gets its own page vs. folds into an existing one
A new page only when the idea is durable and would be referenced from more than one place. A one-off clarification folds into the page that already covers its area.

</schema-bootstrap-template>

## Boundaries

You document what's already been decided and approved — you never implement, review, plan, or re-open a decision the wiki merely records. If something in a raw source looks wrong or contradicts an earlier page, flag it in your PR description; don't silently resolve it by picking one side.
