# CLAUDE.md

This is the workspace repo: everything that lets agents work as members of the organisation. This copy is anonymised: names, domains and accounts are examples. People and agents both work here.

## Layout

| Folder | What it is |
|---|---|
| `ARCHITECTURE.md` | The architecture of the workspace: the vision, the principles and the design (why). Read the relevant section before a change that crosses folders. |
| `docs/` | The infrastructure plan (`plan.md`), the threat model (`threat-model.md`), the onboarding guide and the tutorial. |
| `orchestrator/` | A simplified Multica orchestrator (Go). |
| `claudebox/` | The agent runtime: the ClaudeBox image, the run wrapper and the launcher. |
| `broker/` | The credential broker: the credential service (stop switch, launcher check, GitHub tokens); the assignment service and mail tools come later. |
| `local/` | The workspace on one machine: local stand-ins for the cloud services, and simulated laptops. |
| `.multica/` | This repo's Multica project configuration and workflows. |
| `.claude/` | Hooks and skills for every Claude Code session in this repo. Start Claude Code from the repo root, so that they load. |

## Checks

Run the checks for each folder you change. CI runs the same ones.

| Folder | Command |
|---|---|
| `orchestrator/` | `cd orchestrator && go vet ./... && go test ./... && gofmt -l .` |
| `claudebox/` | `claudebox/tests/run.sh` (offline; `--live` also calls Claude) |
| `broker/` | `cd broker && go vet ./... && go test -race ./... && gofmt -l .` (set `BROKER_TEST_DATABASE_URL` to also test the Postgres stop switch) |
| `local/` | `local/scripts/up.sh`, then `check.sh`, `smoke-l1.sh`, `smoke-l4.sh` and `smoke-threats.sh` in `local/scripts/` |
| Documents | `python3 scripts/check_docs.py` |

## Rules

- A person approves every merge. An agent's approval never counts in this repo, because this code sets the limits that agents work within.
- Never change access rules to give yourself or another agent more access: the broker rules, CODEOWNERS, `.github/`, `.claude/settings.json`, IAM in the cloud account. Propose the change in the pull request description and let a person make it.
- Keep each change inside one folder when you can. When a change must cross folders (for example, the launcher check spans `claudebox/`, `broker/` and `local/`), make it one pull request and say why in the description.
- Write markdown with one line per paragraph. Use Mermaid for diagrams. The `no-hard-wrap` hook rejects hard-wrapped markdown.
