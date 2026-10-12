---
name: compliance-check
description: Check a diff or the running estate for CONFORMANCE to the organisation's GRC control specifications, which are read LIVE from Confluence — never embedded. Use when the compliance agent gates a PR before merge, or runs the periodic full-estate audit.
---

# compliance-check

Custom skill (not from the aihero/mattpocock catalog). You check
CONFORMANCE to the organisation's compliance standard. This is distinct from security
exploitability (that is the security agent's job).

## Source of truth — the in-repo summary

Read the controls from **`docs/compliance/org-standard.md`** in this repo
— a distilled, control-coded, engineering-checkable summary of the organisation's
GRC controls (SDLC, security, access, crypto, data, change). Each control is a
testable MUST tagged with its code (e.g. `SD-2.2-22`, `SS-3.4-01`, `AC-2.2-02`).

- This summary is synced from Confluence (GRC → Control Specifications); its
  header carries the source link + synced date. Treat it as current.
- If `docs/compliance/org-standard.md` is **missing**, STOP and report
  that compliance cannot be verified — never a silent PASS.
- (Future: a live Confluence MCP can replace the static summary. Not required now.)

## Procedure

1. Determine scope — a PR diff (merge gate) or the running estate (periodic audit).
2. Read the controls from `docs/compliance/org-standard.md`. For a PR,
   focus on the code/security/data controls (SDLC, crypto, access, logging,
   privacy); for the estate audit, apply all of them.
3. For each applicable control, decide PASS / REQUEST_CHANGES / N-A against the
   change (or estate state). Skip controls that don't apply — say so, don't inflate.
4. Report each finding citing the **control code and the Confluence page** it came
   from (e.g. "SD-2.2-22 — code review not evidenced on this PR"). Never a vague
   "compliance issue".
5. Verdict: PASS only if no applicable control is violated. On the estate audit,
   raise a ticket to the `orchestrator` per drift found.

## Boundaries

Findings-only: cite the control and hand back; you never edit code or fix drift.
You never cache or embed control text — always read the live source.
