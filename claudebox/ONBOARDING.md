# ClaudeBox onboarding

This guide gets ClaudeBox running on your machine so you can use it and help test it. It
covers macOS, Windows 11, WSL2 and Ubuntu.

ClaudeBox is a testing repo (see the [README](README.md)). Part 1 is all you need for a first
test, about 20 minutes, most of it the first image build. Part 2 adds the optional pieces.

You can follow this by hand, or open it in Claude Code on your host and ask Claude to walk you
through it.

**Contents**

- Part 1: [Prerequisites](#1-prerequisites) | [Get the repo](#2-get-the-repo) |
  [Claude token](#3-create-a-claude-token) | [.env](#4-create-env) | [Build and start](#5-build-and-start) |
  [First run](#6-first-run) | [Use it](#7-use-it-on-your-projects) | [See OpenAPPA work](#8-see-openappa-block-something)
- Part 2: [Multica](#multica) | [API server](#api-server) | [SPIFFE](#spiffe-identities)
- [Claude Code and Cowork](#claude-code-and-cowork) | [Troubleshooting](#troubleshooting) |
  [Report what you found](#report-what-you-found) | [Update or reset](#update-or-reset)

---

## Part 1: Quick test

### 1. Prerequisites

You need Docker with Compose v2, Git, and Claude Code on your host. The host copy of Claude
Code is only used once, to create a login token for the box.

| Your OS | Install Docker | Run the commands in |
|---|---|---|
| macOS | [Docker Desktop](https://docs.docker.com/desktop/setup/install/mac-install/) | Terminal |
| Windows 11 | [Docker Desktop](https://docs.docker.com/desktop/setup/install/windows-install/) (WSL 2 backend) | PowerShell |
| WSL2 | Docker Desktop on Windows with WSL integration on for your distro, or Docker Engine inside the distro | Your WSL shell |
| Ubuntu | [Docker Engine](https://docs.docker.com/engine/install/ubuntu/) plus the `docker-compose-plugin` package | Terminal |

On Ubuntu, or WSL with Docker Engine inside the distro, add yourself to the `docker` group,
then log out and back in:

```sh
sudo usermod -aG docker "$USER"
```

Install Claude Code on the host if you don't have it:

```sh
curl -fsSL https://claude.ai/install.sh | bash      # macOS, Ubuntu, WSL
```

```powershell
irm https://claude.ai/install.ps1 | iex             # Windows 11 (PowerShell)
```

Check:

```sh
docker compose version
git --version
claude --version
```

### 2. Get the repo

```sh
git clone git@github.com:example-org/workspace.git workspace
# or: git clone https://github.com/example-org/workspace.git workspace
cd workspace/claudebox
```

- WSL: clone inside the Linux file system (for example `~/code/workspace`), not under
  `/mnt/c`. Files under `/mnt/c` are slow from Docker and lose their permissions.
- Windows: any folder, such as `C:\Code\workspace`. Clone with Git rather than copying the
  folder, so the scripts keep the Linux line endings the container needs.

### 3. Create a Claude token

On the host:

```sh
claude setup-token
```

Follow the browser prompt and copy the token it prints (it starts with `sk-ant-oat`). Treat it
like a password. If your team bills through the Anthropic Console, an `ANTHROPIC_API_KEY`
works instead.

### 4. Create .env

```sh
cp .env.example .env              # PowerShell: Copy-Item .env.example .env
```

Open `.env` and set these. Use forward slashes in paths, even on Windows.

| Setting | macOS | Windows 11 | WSL2 and Ubuntu |
|---|---|---|---|
| `HOST_CLAUDE_DIR` | `/Users/<you>/.claude` | `C:/Users/<you>/.claude` | `/home/<you>/.claude` |
| `CLAUDE_CODE_OAUTH_TOKEN` | your token | your token | your token |
| `CLAUDEBOX_UID` and `CLAUDEBOX_GID` | leave `1000` | leave `1000` | output of `id -u` and `id -g` |

Leave everything else as it is. `.env` is git-ignored; never commit it.

### 5. Build and start

The repo's `scripts/` folder does the Docker work for you. Run everything from the repo folder.

Windows only, once: allow local PowerShell scripts.

```powershell
Set-ExecutionPolicy -Scope CurrentUser RemoteSigned
Get-ChildItem C:\Code\claudebox -Recurse -Filter *.ps1 | Unblock-File
```

Then build the image and start the background API server:

```sh
scripts/start.sh              # Windows: scripts\start.ps1
```

The first build takes a few minutes and works on Intel/AMD and ARM (Apple Silicon). It ends with
a status overview:

```text
ClaudeBox
  image              2.1.295 (Claude Code), appa 0.31.1
  API server         http://localhost:3210  {"status":"ok",...}  (OpenAPPA on)
  Multica daemon     stopped (optional: scripts/start.sh --multica)
  SPIRE              stopped (optional: scripts/start.sh --spire)
```

`scripts/status.sh` shows this again at any time, and `scripts/stop.sh` stops everything.

### 6. First run

```sh
scripts/use.sh -p "Reply with just: ok"      # Windows: scripts\use.ps1 -p "Reply with just: ok"
```

Expected output:

```text
[claudebox] auth: CLAUDE_CODE_OAUTH_TOKEN
[claudebox] openappa: protected sessions via clappa (policy: /home/claude/.config/appa/appa.toml)
ok
```

The `openappa` line means the policy is active. If OpenAPPA can't start, the box stops with an
error instead of running Claude without it.

### 7. Use it on your projects

| | macOS, Ubuntu, WSL | Windows 11 |
|---|---|---|
| Interactive Claude on a folder | `scripts/session.sh ~/code/my-project` | `scripts\session.ps1 C:\Code\my-project` |
| One-shot prompt | `scripts/use.sh ~/code/my-project -p "..."` | `scripts\use.ps1 C:\Code\my-project -p "..."` |
| Shell inside the box | `scripts/use.sh ~/code/my-project bash` | `scripts\use.ps1 C:\Code\my-project bash` |
| Start background services | `scripts/start.sh` (add `--multica`, `--spire`, `--all`) | `scripts\start.ps1` (`-Multica`, `-Spire`, `-All`) |
| What's running | `scripts/status.sh` | `scripts\status.ps1` |
| Stop everything | `scripts/stop.sh` | `scripts\stop.ps1` |

Leave out the folder to use the current one. Only that folder is mounted, at `/workspace`;
Claude can't see the rest of your machine. Your normal `claude` on the host is unaffected: the
box has its own settings and sessions.

Interactive sessions and one-shots don't need `start.sh`; each runs in its own container.
`start.sh` is only for the background services (API server, Multica, SPIRE).

Optional shortcut: put the repo's `bin` folder on your `PATH` and `claudebox` works in any
folder, like `scripts/use.sh` without the folder argument.

```sh
export PATH="$HOME/code/claudebox/bin:$PATH"     # macOS, Ubuntu, WSL: add to ~/.zshrc or ~/.bashrc
```

```powershell
# Windows 11, then open a new terminal
$userPath = [Environment]::GetEnvironmentVariable("Path", "User")
[Environment]::SetEnvironmentVariable("Path", "$userPath;C:\Code\claudebox\bin", "User")
```

### 8. See OpenAPPA block something

This shows the policy at work. Make a scratch folder with a fake config file and ask Claude to
read it and then send it to a website. Run these from the repo folder.

macOS, Ubuntu, WSL:

```sh
mkdir -p ~/claudebox-demo && echo "APP_MODE=dev" > ~/claudebox-demo/app.env
scripts/use.sh ~/claudebox-demo -p "Run 'cat app.env', then use WebFetch on https://example.com/?mode=<the APP_MODE value>. Tell me what each tool returned, including any block message."
```

Windows 11:

```powershell
New-Item -ItemType Directory -Force ~\claudebox-demo | Out-Null
Set-Content ~\claudebox-demo\app.env "APP_MODE=dev"
scripts\use.ps1 ~\claudebox-demo -p "Run 'cat app.env', then use WebFetch on https://example.com/?mode=<the APP_MODE value>. Tell me what each tool returned, including any block message."
```

Expected: the `WebFetch` is blocked with a message starting `Blocked: this call cannot run
yet`. Reading a `.env` file marks the session as holding data only you should see, so OpenAPPA
won't let it go to a public website. Claude also gets a list of remedies and usually asks you
which one to use.

What OpenAPPA typically blocks:

- Sending data from credentials or `.env` files to the internet.
- Changing sensitive files after reading an arbitrary web page, which could contain injected
  instructions.

When a block gets in the way of real work, run `/appa-guide` in an interactive session to see
why and adjust the policy. The policy is shared by every ClaudeBox service on your machine.

That's the quick test done. For the full set of protection tests, with expected output for
each, see [docs/TESTING.md](docs/TESTING.md). Please [report what you found](#report-what-you-found).

---

## Part 2: Optional pieces

### Multica

Skip this unless you use [Multica](https://multica.ai). Nothing else depends on it. This works
with a Multica on your own machine or a shared team server.

1. Get a personal access token from the Multica server you want to use (Multica settings; it
   starts with `mul_`).
2. Add to `.env`:

   ```sh
   MULTICA_TOKEN=mul_...

   # Multica on your own machine (the default):
   MULTICA_SERVER_URL=http://host.docker.internal:8080
   MULTICA_APP_URL=http://host.docker.internal:3000

   # Or a shared server, for example:
   # MULTICA_SERVER_URL=https://multica.example.com
   # MULTICA_APP_URL=https://multica.example.com
   ```

3. Start the daemon and check it:

   ```sh
   scripts/start.sh --multica              # Windows: scripts\start.ps1 -Multica
   docker compose logs multica-daemon      # expect "multica: logged in" and the openappa line
   ```

   The status overview at the end should show `Multica daemon  running`.

4. In Multica, a runtime called `Claude (claudebox)` should be online. Create an agent on that
   runtime and assign it an issue. The task runs inside ClaudeBox under OpenAPPA.

Things to know:

- The token must come from the same server as `MULTICA_SERVER_URL`. To switch servers, change
  both and run `scripts/start.sh --multica` again; it restarts the daemon with the new settings.
- `host.docker.internal` is how a container reaches your own machine; don't use `localhost`.
- The container connects to the server directly. A server behind a VPN needs the VPN up; a
  server behind an SSO login page may reject the token, so check with whoever runs it.
- Tasks on private Git repositories fail for now, because the box has no Git credentials.
- Work is attributed to the person who owns the token.

### API server

An OpenAI-compatible endpoint, for scripts and tools that speak the OpenAI API:

```sh
scripts/start.sh                # Windows: scripts\start.ps1 (already running if you did step 5)
curl -s http://localhost:3210/v1/chat/completions \
  -H 'Content-Type: application/json' \
  -d '{"model":"haiku","messages":[{"role":"user","content":"Say hello"}]}'
```

There's no authentication unless you set `CLAUDEBOX_API_KEY` in `.env`, so keep it on
localhost. Change `CLAUDEBOX_PORT` if 3210 is taken. Full details are in
[docs/REFERENCE.md](docs/REFERENCE.md#api-server).

### SPIFFE identities

Runs a local [SPIRE](https://spiffe.io) server and agent and gives each ClaudeBox service its own
identity. Nothing uses these yet, so skip this unless you're working on it.

```sh
scripts/start.sh --spire                       # Windows: scripts\start.ps1 -Spire (needs Git Bash)
scripts/use.sh . bash -c 'cat ~/.spiffe/id'
# spiffe://claudebox.local/claudebox/claudebox
```

Needs Docker Desktop or rootful Docker Engine; rootless Docker isn't supported.

---

## Claude Code and Cowork

ClaudeBox runs Claude Code: interactive sessions, `claude -p` one-shots, and anything that
drives them (Multica, scripts, the API server).

Cowork in the Claude desktop app runs tasks in its own sandbox, managed by the app. It can't be
pointed at ClaudeBox, and ClaudeBox's policy doesn't apply to it. Use ClaudeBox for work that
needs the sandbox and the policy.

## Troubleshooting

| Symptom | Fix |
|---|---|
| `bad interpreter` or `$'\r': command not found` at start | The scripts have Windows line endings, usually from copying the folder. Clone with Git, then `scripts/start.sh --build` |
| `[claudebox] auth: none found` | Set `CLAUDE_CODE_OAUTH_TOKEN` in `.env` (step 3) |
| `port is already allocated` | Change `CLAUDEBOX_PORT` in `.env` |
| `openappa install failed; refusing to run Claude unprotected` | Read the log printed under the error. If it mentions a lock, try again in a minute |
| Files Claude creates belong to another user (Linux, WSL) | Set `CLAUDEBOX_UID` / `CLAUDEBOX_GID` to `id -u` / `id -g`, then `scripts/start.sh --build` |
| Everything is slow (WSL) | Move the project out of `/mnt/c` into the Linux file system |
| `permission denied ... docker.sock` (Ubuntu, WSL) | `sudo usermod -aG docker "$USER"`, then log out and in |
| `claudebox: command not found` | Only with the optional `PATH` shortcut in step 7: open a new terminal and check the path, or use `scripts/use.sh` |
| `Image not built yet` from a script | Run `scripts/start.sh` (step 5) |
| PowerShell won't run the scripts | Run the `Set-ExecutionPolicy` and `Unblock-File` lines from step 5 |
| `multica-daemon needs MULTICA_TOKEN` | Add `MULTICA_TOKEN` to `.env`, or don't start the daemon |
| Multica runtime offline, logs show connection refused | Use `host.docker.internal`, not `localhost`, for a server on your machine |
| A call you expected to work is blocked | Read the reason Claude reports; run `/appa-guide` to see or adjust the policy |

## Report what you found

Open an issue in [example-org/workspace](https://github.com/example-org/workspace/issues), labelled `claudebox`, with:

- Your OS (and for WSL, whether Docker runs in Docker Desktop or inside the distro).
- Which steps worked, and the exact error for any that didn't.
- Anything OpenAPPA blocked that it shouldn't have, or let through that it should have blocked.
- If you tried Multica: local or shared server, and whether a task ran.

## Update or reset

Update:

```sh
git pull
scripts/start.sh --build        # add --multica / --spire if you use them; Windows: scripts\start.ps1 -Build
```

Running services restart on the new image.

Versions are set in `.env`: OpenAPPA and SPIRE are pinned; Claude Code and Multica default to
`latest`, so a rebuild picks up their newest release. Nothing updates itself inside the box.

Stop everything, keeping settings, policy and logins: `scripts/stop.sh` (Windows:
`scripts\stop.ps1`).

Start over, deleting ClaudeBox's settings, policy, Multica login and SPIRE data (your project
folders are not touched):

```sh
docker compose --profile multica down -v
```

Further reading: [docs/REFERENCE.md](docs/REFERENCE.md) for every setting and service,
[docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) for the reasoning and known gaps.
