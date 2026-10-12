# ClaudeBox reference

Detailed behaviour of each part. For setup, see [ONBOARDING.md](../ONBOARDING.md). For the
reasoning behind the design, see [ARCHITECTURE.md](ARCHITECTURE.md).

- [Scripts](#scripts)
- [CLI](#cli)
- [API server](#api-server)
- [OpenAPPA](#openappa)
- [Multica runtime](#multica-runtime)
- [SPIFFE / SPIRE](#spiffe--spire)
- [Authentication](#authentication)
- [Configuration (.env)](#configuration-env)
- [Volumes](#volumes)
- [Notes and limits](#notes-and-limits)

## Scripts

Convenience wrappers around `docker compose`, in `scripts/`. Each has a `.sh` version (macOS,
Linux, WSL, Git Bash) and a `.ps1` version (Windows PowerShell) with the same behaviour.

| Script | What it does |
|---|---|
| `start.sh [--multica] [--spire] [--all] [--build]` | Checks `.env`, builds the image if missing, starts the API server (plus the Multica daemon and/or SPIRE), waits for health, then shows status. PowerShell: `-Multica -Spire -All -Build`; `-Spire` needs bash (Git Bash) for `spire/bootstrap.sh` |
| `stop.sh` | Stops every service, including the `multica` profile. Volumes are kept |
| `status.sh` | Image versions, API health and OpenAPPA state, Multica daemon state, SPIRE |
| `session.sh [folder] [args]` | Interactive Claude session on `folder` (default: current). Refuses `-p` and non-terminal input. Arguments go to Claude, for example `--model opus` or `--resume` |
| `use.sh [folder] [args]` | Runs Claude on `folder` (default: current) through `bin/claudebox`. Arguments go to Claude, or run as a command (`bash`) |

```sh
scripts/start.sh --all
scripts/use.sh ~/code/my-project -p "summarise this repo"
scripts/stop.sh
```

Sessions (`session.sh`, `use.sh`) don't need `start.sh` first; each runs in its own container.

Every project is mounted at `/workspace` and Claude's session history lives in the shared
`claudebox-config` volume, so `--continue` resumes the most recent session from any project.
Use `--resume` and pick the session you want.

## CLI

`scripts/session.sh` (interactive) and `scripts/use.sh` (one-shot or shell) run the box on a
folder; see [Scripts](#scripts). `bin/claudebox` (macOS, Linux, WSL) and `bin/claudebox.ps1`
(Windows) do the same on the current folder when on your `PATH`. Without either:

```sh
docker compose run --rm claudebox                               # interactive, project = this folder
PROJECT_DIR=~/code/other docker compose run --rm claudebox      # another folder (Windows: C:/Code/other)
docker compose run --rm claudebox -p "summarise this repo"      # one-shot
docker compose run --rm claudebox bash                          # shell inside the box
```

Claude runs with `--dangerously-skip-permissions`, because the container and OpenAPPA are
the controls. Set `CLAUDEBOX_SKIP_PERMISSIONS=0` in `.env` to get Claude Code's own prompts
back in the CLI.

## API server

An OpenAI-compatible HTTP API, adapted from
[ArmanJR/claudebox](https://github.com/ArmanJR/claudebox). Its upstream source is vendored in
`claudebox/` for reference only.

```sh
scripts/start.sh                          # http://localhost:${CLAUDEBOX_PORT}, default 3210
scripts/status.sh                         # health and OpenAPPA state
docker compose logs -f claudebox-server
scripts/stop.sh
```

| Method | Path | Notes |
|---|---|---|
| GET | `/health` | `{"status":"ok","activeRequests":n,"openaiCompat":true}` |
| GET | `/v1/models` | Static list of Claude ids and aliases (edit `MODELS` in `server.js`) |
| POST | `/v1/chat/completions` | System messages, `response_format` (json_schema / json_object), base64 `image_url` parts, tools, `stream` |
| POST | `/prompt` | Raw Claude Code JSON result; body `{"prompt": "...", "options": {...}}` |

Each request runs `clappa -p --dangerously-skip-permissions --no-session-persistence` in
`/workspace`, so the model can read and edit the mounted project under OpenAPPA. Send a Claude
model id or alias (`haiku`, `sonnet`, `opus`) in `model`; OpenAI model names fail.

```sh
curl -s http://localhost:3210/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"haiku","messages":[{"role":"user","content":"Say hello"}]}'
```

```python
from openai import OpenAI
client = OpenAI(base_url="http://localhost:3210/v1", api_key="unused-or-CLAUDEBOX_API_KEY")
r = client.chat.completions.create(model="sonnet", messages=[{"role": "user", "content": "Say hello"}])
print(r.choices[0].message.content)
```

Settings: `CLAUDEBOX_PORT` (host port), `CLAUDEBOX_API_KEY` (if set, `/v1/*` requires
`Authorization: Bearer <key>`), `MAX_CONCURRENT` (parallel runs, default 4). Without an API
key the server is open to anything that can reach the port, so keep it on localhost.

### Function calling

- `tools: [{type: "function", function: {name, description, parameters}}]` (legacy `functions` too)
- `tool_choice`: `"auto"` (default), `"none"`, `"required"`, or `{type: "function", function: {name}}`
- Responses with calls have `finish_reason: "tool_calls"` and `message.tool_calls[]`
- Send results back as `{role: "tool", tool_call_id, content}` messages and call again
- `stream: true` returns SSE chunks (the whole answer in one delta, then `[DONE]`);
  `stream_options.include_usage` adds a usage chunk

Tool calls are native. For each request with `tools`, the server registers the functions as
MCP tools in the Claude session (`mcp-bridge.js`, server name `client`), captures Claude's
real `tool_use` blocks from the `stream-json` output, ends the session, and returns them as
`tool_calls`. The bridge never answers a call: your client executes the function, outside the
container and outside OpenAPPA's view. Validate arguments before executing.
`CLAUDEBOX_BUILTIN_TOOLS` (for example `""` or `"Read,Grep"`) limits which built-in Claude
Code tools stay available during `/v1` requests.

## OpenAPPA

[OpenAPPA](https://github.com/archestra-ai/OpenAPPA) is mandatory in every service. On each
start the entrypoint runs `appa plugin install claude-code` (idempotent) and launches Claude
through `clappa`, which switches the hooks on for that session. If the install fails, the
container exits instead of running Claude unprotected.

The image also installs OpenAPPA's hooks as Claude Code managed settings (`/etc/claude-code/managed-settings.json`, with `allowManagedHooksOnly`). A `.claude/settings.json` in a project cannot switch them off, and no other hooks run: not your own, not a project's.

| Item | Location |
|---|---|
| Policy (shared by all services) | `/home/claude/.config/appa/appa.toml`, volume `claudebox_appa-config` |
| Runtime data, event log (per service) | `/home/claude/.local/share/appa`, volumes `claudebox_appa-data-{cli,server,daemon}` |
| `clappa` launcher | `/home/claude/.local/bin/clappa` |
| Install log | `/home/claude/.local/share/appa/install.log` |

Change the policy with `/appa-guide` inside a CLI session, or edit the file from
`claudebox bash`. Restart running services afterwards. Test changes without running tools:

```sh
appa describe --config ~/.config/appa/appa.toml --check
appa replay --config ~/.config/appa/appa.toml /workspace/tests/policy    # run from the repo folder
```

A blocked call returns a reason and a remedy plan to Claude. For example, reading a `*.env*`
file narrows the session to `self`, after which a `WebFetch` to the internet is refused.

The version is pinned with `APPA_VERSION` and its checksum is verified at build time.

## Multica runtime

Optional. The `multica-daemon` service is in the `multica` compose profile, so it starts only
when named, and exits with a clear error if it has never logged in (no `MULTICA_TOKEN` and no
earlier login in the `multica-state` volume).

```sh
scripts/start.sh --multica                   # or: docker compose up -d multica-daemon
docker compose exec multica-daemon multica daemon status
multica runtime list                         # on the host: Claude (claudebox) ... online
```

- The daemon reads its token from `~/.multica/config.json`, not the environment. The
  entrypoint runs `multica login --token "$MULTICA_TOKEN"` on every start, so changes to the
  token or server in `.env` apply after `scripts/start.sh --multica`, which recreates the
  daemon when its settings changed.
- `MULTICA_SERVER_URL` / `MULTICA_APP_URL` default to `host.docker.internal`, a Multica on the
  same machine. Point them at a shared server's URL to use that instead.
- Claude is started through `clappa` (`MULTICA_CLAUDE_PATH`), so tasks run under OpenAPPA.
- Daemon ID and device name are `claudebox`, so it never collides with a daemon on the host.
- Task checkouts live in the `claudebox_multica-workspaces` volume.
- Self-update is off. Bump `MULTICA_VERSION` and rebuild to upgrade.
- There are no Git credentials in the container, so private repositories fail to clone.

## SPIFFE / SPIRE

Optional. A local SPIRE server and agent, trust domain `claudebox.local`, issue each service
its own SPIFFE ID, matched on the container's compose labels.

| Service | SPIFFE ID |
|---|---|
| `claudebox` | `spiffe://claudebox.local/claudebox/claudebox` |
| `claudebox-server` | `spiffe://claudebox.local/claudebox/claudebox-server` |
| `multica-daemon` | `spiffe://claudebox.local/claudebox/multica-daemon` |

```sh
scripts/start.sh --spire   # runs spire/bootstrap.sh: starts SPIRE, joins the agent, registers the IDs
scripts/stop.sh && scripts/start.sh --spire   # services already running pick up SVIDs on restart
```

When the agent socket is present, the entrypoint writes the X.509 SVID, key and trust bundle
to `~/.spiffe/` (`svid.0.pem`, `svid.0.key`, `bundle.0.pem`, plus `id`), exports `SPIFFE_ID`,
and refreshes them every 20 minutes. Without SPIRE, containers start normally. JWT-SVIDs on
demand:

```sh
spire-agent api fetch jwt -audience my-service -socketPath /run/spire/sockets/agent.sock
```

- The agent needs `pid: host` and the Docker socket (read-only) for the docker workload
  attestor. Rootless Docker is not supported.
- `insecure_bootstrap = true` in `spire/agent.conf` is for local testing only.
- Nothing consumes the SVIDs yet.

## Authentication

The entrypoint uses the first of these that is available:

1. `CLAUDE_CODE_OAUTH_TOKEN` in `.env`, from `claude setup-token` on the host. Recommended on
   every OS: long-lived, and the container never refreshes anything.
2. `ANTHROPIC_API_KEY` in `.env`, for Console API billing.
3. Credentials already in the `claudebox_claudebox-config` volume.
4. The host's `~/.claude/.credentials.json`, mounted read-only through `HOST_CLAUDE_DIR` and
   copied in on first run. Windows and Linux only; macOS keeps credentials in the Keychain. The
   container's copy can drift from the host's when tokens rotate, which option 1 avoids.

With `CLAUDEBOX_SEED_SETTINGS=1`, the host `settings.json` is copied in on first run. The
default is `0`, because host hooks and status lines usually call programs the container lacks.

To drop the container's Claude config and start from the host again:

```sh
scripts/stop.sh
docker volume rm claudebox_claudebox-config
```

## Configuration (.env)

| Variable | Default | Purpose |
|---|---|---|
| `HOST_CLAUDE_DIR` | `~/.claude` | Host Claude config folder, mounted read-only |
| `PROJECT_DIR` | `.` | Folder mounted at `/workspace` (the launchers set it to the current folder) |
| `CLAUDE_CODE_OAUTH_TOKEN` | | Claude login token |
| `ANTHROPIC_API_KEY` | | Alternative to the token |
| `CLAUDEBOX_UID` / `CLAUDEBOX_GID` | `1000` | Container user; set to your `id -u` / `id -g` on Linux and WSL |
| `CLAUDEBOX_SEED_SETTINGS` | `0` | Copy host `settings.json` on first run |
| `CLAUDEBOX_SKIP_PERMISSIONS` | `1` | `0` brings back Claude Code's prompts in the CLI |
| `CLAUDEBOX_PORT` | `3210` | API server host port |
| `CLAUDEBOX_API_KEY` | | Bearer key for `/v1/*` |
| `MAX_CONCURRENT` | `4` | Parallel API runs |
| `CLAUDEBOX_BUILTIN_TOOLS` | all | Built-in Claude tools available to `/v1` requests (`"Read,Grep"`, or `""` for none) |
| `MULTICA_TOKEN` | | Multica personal access token; empty = no Multica |
| `MULTICA_SERVER_URL` / `MULTICA_APP_URL` | `host.docker.internal:8080` / `:3000` | Multica server as seen from the container |
| `MULTICA_WORKSPACE_ID` | | Limit the daemon to one workspace |
| `CLAUDE_CODE_VERSION`, `MULTICA_VERSION` | `latest` | Versions baked into the image (finding F4) |
| `APPA_VERSION`, `SPIRE_VERSION` | `v0.31.1`, `1.15.3` | Pinned versions baked into the image |
| `TZ` | `UTC` | Container time zone |

## Volumes

All names are prefixed `claudebox_` because the compose project name is pinned.

| Volume | Holds |
|---|---|
| `claudebox-config` | Claude Code config and sessions inside the box |
| `appa-config` | OpenAPPA policy and batteries |
| `appa-data-cli`, `appa-data-server`, `appa-data-daemon` | OpenAPPA runtime, event log, `clappa`, per service |
| `multica-state`, `multica-workspaces` | Multica login and task checkouts |
| `spire-server-data`, `spire-agent-data`, `spire-agent-socket` | SPIRE state and the Workload API socket |

`docker compose --profile multica down -v` removes all of them. Your project folders are bind
mounts and are never touched.

## Notes and limits

- Claude runs as the unprivileged user `claude`. Only `/workspace` and the volumes above
  persist; anything else Claude changes is gone when the container exits.
- Network egress is not restricted. Upstream ArmanJR/claudebox (vendored in `claudebox/`) has
  an iptables allowlist (`claudebox/allowed-domains.txt`) that could be ported.
- `.gitattributes` keeps scripts in LF line endings so a Windows clone runs in Linux.
- Nothing updates itself. Bump versions in `.env` and run `scripts/start.sh --build`.
- [ARCHITECTURE.md](ARCHITECTURE.md#known-gaps) lists the known gaps.
