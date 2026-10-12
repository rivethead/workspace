# Vendored skills

Agent skills, **vendored** (copied in) not installed at setup — so the exact
tested version travels with the repo, works offline, and can't drift when
upstream changes. A coding agent auto-discovers all of them here, whether you're
working the repo locally or it was dispatched to by Multica.

Two things use these skills, and they draw on different subsets:

## Deployed to Multica agents (the roster subset)

`onboard apply` imports **only** the skills referenced by roles in
`.multica/fleet.yaml`, and assigns each to its agent. That set is intentionally
specific — an agent gets exactly the skills its role calls for:

- `grill-with-docs`, `research`, `domain-modeling` — analyst / architect
- `grilling` — advocate (red-team)
- `codebase-design` — architect
- `to-spec`, `to-tickets`, `wayfinder` — planner
- `to-wiki` — librarian
- `compliance-check` — compliance

The security role deliberately uses no vendored skill: it does its
exploitability review directly, plus whatever static-analysis the project wires
into CI. Assign a role a new skill by adding it to that role's `skills:` in
`.multica/fleet.yaml` — if the skill is present in this directory, the next
`apply` deploys it.

## Available for local coding (everything else here)

The remaining skills — `tdd`, `implement`, `code-review`,
`resolving-merge-conflicts`, `diagnosing-bugs`, `improve-codebase-architecture`,
`playwright-skill`, `triage`, `handoff`, `prototype`, `setup-pre-commit`,
`git-guardrails-claude-code`, `ask-matt` — ship in the repo for the local coding
experience (and for the build-lifecycle roles that are currently shelved). They
are **not** deployed to Multica agents until a role references them. Present, not
pushed.

## Provenance

Each skill is pinned. All MIT.

- **aihero / `mattpocock/skills`** — pinned `9603c1cc8118d08bc1b3bf34cf714f62178dea3b`.
  All of `skills/engineering/*` (minus the installer) + `productivity/{grilling,handoff}`
  + `misc/{git-guardrails-claude-code,setup-pre-commit}`.
- **`playwright-skill`** — `lackeyjb/playwright-skill`, pinned
  `bb7e920d376022958214e349ef25498a2644e189`. Browser E2E; runtime needs
  `npx playwright install --with-deps`.
- **`compliance-check`** — custom (ours, not upstream). Reads the GRC
  controls live from Confluence; embeds no policy text. Maintained here, no pin.

## Updating

Re-vendoring is a deliberate, reviewed change — a PR that bumps the pinned SHA
and shows the skill diff — never an automatic upstream pull.
