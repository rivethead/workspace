# ClaudeBox



ClaudeBox runs [Claude Code](https://code.claude.com) inside a Docker container that can only
see the project folder you give it. Every Claude session in the box runs under OpenAPPA, a
policy engine that checks each tool call before it happens. Optionally, the box can also take
work from Multica and carry a SPIFFE identity.

We use it to answer a practical question: can we hand agents real work, including work
assigned through Multica, while controlling what they can touch, what they can do with what
they read, and knowing which agent did what?

## Get started

Follow [ONBOARDING.md](ONBOARDING.md). It covers macOS, Windows 11, WSL2 and Ubuntu, and gets
you to a working session in about 20 minutes. The short version:

```sh
cp .env.example .env          # add your Claude token and HOST_CLAUDE_DIR
scripts/start.sh              # builds the image, starts the API server, shows status
scripts/session.sh ~/code/my-project    # interactive Claude on that folder
```

Windows: the same scripts exist as `scripts\start.ps1`, `scripts\session.ps1` and so on.

## Test it

[docs/TESTING.md](docs/TESTING.md) shows step by step how to configure ClaudeBox and OpenAPPA,
then checks each protection with the expected output for every step:

- ClaudeBox protection (S1 to S7): your other files are invisible, only the project folder is
  shared, no root or Docker access, host config is read-only, and changes outside the project
  don't survive. It also shows what isn't covered: outbound network access.
- OpenAPPA protection (A1 to A7): secrets can't be sent to the internet, untrusted web content
  needs your consent, normal work isn't blocked, policy tests replay deterministically, Claude
  won't start without OpenAPPA, and your own rules take effect.

For a quick automated check, run `tests/run.sh` (no model calls) or `tests/run.sh --live`.

[docs/OWASP-LLM-TOP10.md](docs/OWASP-LLM-TOP10.md) maps ClaudeBox against the OWASP Top 10 for
LLM Applications (2025): which risks apply, how to test each one, and what is still open.

## Technology

| | What it is | Role here | Required |
|---|---|---|---|
| ClaudeBox | This repo: Claude Code packaged in a Docker image, with an OpenAI-compatible API server adapted from [ArmanJR/claudebox](https://github.com/ArmanJR/claudebox) | The sandbox: one mounted folder, unprivileged user, no access to the rest of your machine | Yes |
| [Claude Code](https://code.claude.com) | Anthropic's agentic coding tool for the terminal | The agent that does the work inside the box | Yes |
| [OpenAPPA](https://openappa.com) ([GitHub](https://github.com/archestra-ai/OpenAPPA)) | Open-source, deterministic guardrails for agents, by Archestra. Tracks how sensitive and how trustworthy the data a session has read is, and checks every tool call against a TOML policy | The policy layer: blocks things like sending secrets to the internet or acting on instructions from an untrusted web page | Yes, always on |
| [Multica](https://multica.ai) ([GitHub](https://github.com/multica-ai/multica)) | Open-source, self-hostable platform where people and AI agents work on issues together; a local daemon runs agents on your machine | The scheduler: its daemon runs inside the box and hands it tasks | No |
| [SPIFFE](https://spiffe.io) / [SPIRE](https://github.com/spiffe/spire) | SPIFFE is a CNCF standard for workload identity; SPIRE is its reference implementation, which issues short-lived, auto-rotated certificates and tokens to workloads | The identity layer: each ClaudeBox service gets its own verifiable ID. Groundwork only, nothing checks it yet | No |

## What's in the repo

| Path | What it is |
|---|---|
| `ONBOARDING.md` | Setup and testing guide for team members |
| `docs/ARCHITECTURE.md` | Why it is built this way, what was verified, known gaps |
| `docs/REFERENCE.md` | Details of every service, setting and volume |
| `docs/TESTING.md` | Step-by-step configuration and tests for the sandbox and OpenAPPA |
| `docs/OWASP-LLM-TOP10.md` | OWASP LLM Top 10 mapping, tests and findings |
| `tests/` | Automated checks (`tests/run.sh`) and OpenAPPA policy tests (`tests/policy/`; `tests/policy-live/` call the model, so they run with `--live`) |
| `appa/workspace.toml` | the organisation's OpenAPPA rules. The image puts them before the default rules, and a new policy starts from them. For example, output of the `multica` CLI is trusted, because only members of the organisation write in the workspace. A `multica` command chained with other commands goes to a model annotator, with a hint that says this |
| `Dockerfile`, `entrypoint.sh`, `docker-compose.yml` | The image, its startup logic, and the services |
| `scripts/` | Start, stop, check and use ClaudeBox (`start`, `stop`, `status`, `session`, `use`; `.sh` and `.ps1`) |
| `bin/claudebox`, `bin/claudebox.ps1` | Run ClaudeBox on the current folder (macOS/Linux/WSL, Windows) |
| `server.js`, `mcp-bridge.js` | OpenAI-compatible API server and its tool bridge |
| `spire/` | Local SPIRE configuration and the bootstrap script |
| `claudebox/` | Vendored upstream ArmanJR/claudebox, for reference only; not used in the build |

## Services

| Service | Starts with | Purpose |
|---|---|---|
| `claudebox` | `scripts/session.sh [folder]` (interactive), `scripts/use.sh [folder] -p "..."` (one-shot) | Claude Code CLI |
| `claudebox-server` | `scripts/start.sh` | OpenAI-compatible API on `localhost:3210` |
| `multica-daemon` | `scripts/start.sh --multica` | Multica agent runtime (optional, needs a Multica token) |
| `spire-server`, `spire-agent` | `scripts/start.sh --spire` | SPIFFE identities (optional) |

`scripts/status.sh` shows what's running and healthy; `scripts/stop.sh` stops everything and keeps
your settings, policy and logins.

## Status

Tested on macOS (Apple Silicon) with Docker Desktop: CLI, API server, OpenAPPA blocking,
Multica runtime registration and SPIFFE identities. Not yet tested on Windows 11, WSL2 or
Ubuntu, and no Multica task has yet run end to end under OpenAPPA. If you test on one of those,
work through [docs/TESTING.md](docs/TESTING.md) and send in its results checklist. See
[known gaps](docs/ARCHITECTURE.md#known-gaps) before relying on any of it.
