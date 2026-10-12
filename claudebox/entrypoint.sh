#!/usr/bin/env bash
# claudebox entrypoint: make host authentication available to Claude Code, then run it.
#
# Precedence (first match wins):
#   1. CLAUDE_CODE_OAUTH_TOKEN  - long-lived token from `claude setup-token` on the host
#   2. ANTHROPIC_API_KEY        - Console API key
#   3. Credentials already present in the config volume (from a previous login/seed)
#   4. Host credentials file mounted read-only at $HOST_CREDENTIALS -> seeded into the volume
set -euo pipefail

CONFIG_DIR="${CLAUDE_CONFIG_DIR:-$HOME/.claude}"
HOST_CREDENTIALS="${HOST_CREDENTIALS:-/run/host-claude/.credentials.json}"
HOST_SETTINGS="${HOST_SETTINGS:-/run/host-claude/settings.json}"

mkdir -p "$CONFIG_DIR"

if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ]; then
    echo "[claudebox] auth: CLAUDE_CODE_OAUTH_TOKEN" >&2
elif [ -n "${ANTHROPIC_API_KEY:-}" ]; then
    echo "[claudebox] auth: ANTHROPIC_API_KEY" >&2
elif [ -s "$CONFIG_DIR/.credentials.json" ]; then
    echo "[claudebox] auth: existing credentials in $CONFIG_DIR" >&2
elif [ -r "$HOST_CREDENTIALS" ]; then
    install -m 600 "$HOST_CREDENTIALS" "$CONFIG_DIR/.credentials.json"
    echo "[claudebox] auth: seeded credentials from host" >&2
else
    echo "[claudebox] auth: none found; you will be asked to log in" >&2
fi

# Optional (CLAUDEBOX_SEED_SETTINGS=1): copy host settings.json on first run only
# (never overwrite container edits). Off by default: host hooks rarely work in the box.
if [ "${CLAUDEBOX_SEED_SETTINGS:-0}" = "1" ] && [ -r "$HOST_SETTINGS" ] && [ ! -e "$CONFIG_DIR/settings.json" ]; then
    cp "$HOST_SETTINGS" "$CONFIG_DIR/settings.json"
    echo "[claudebox] seeded settings.json from host" >&2
fi

# Interactive claude runs first-run onboarding (theme + login screens) unless
# .claude.json says it is done, even when valid credentials exist. Mark it done
# whenever we have some form of auth, so the box never asks to sign in.
if [ -n "${CLAUDE_CODE_OAUTH_TOKEN:-}" ] || [ -n "${ANTHROPIC_API_KEY:-}" ] || [ -s "$CONFIG_DIR/.credentials.json" ]; then
    node -e '
const fs = require("fs"), p = process.argv[1];
let j = {};
try { j = JSON.parse(fs.readFileSync(p, "utf8")); } catch {}
if (j.hasCompletedOnboarding !== true) {
    j.hasCompletedOnboarding = true;
    fs.writeFileSync(p, JSON.stringify(j, null, 2), { mode: 0o600 });
}' "$CONFIG_DIR/.claude.json" || echo "[claudebox] warning: could not mark onboarding complete" >&2
fi

# Multica: the daemon reads its token from ~/.multica/config.json, not the environment.
# Log in from MULTICA_TOKEN on every start, so changing the token or server in .env takes
# effect without wiping the multica-state volume.
if [ -n "${MULTICA_TOKEN:-}" ] && command -v multica >/dev/null; then
    multica login --token "$MULTICA_TOKEN" </dev/null >/dev/null 2>&1 \
        && echo "[claudebox] multica: logged in to ${MULTICA_SERVER_URL:-default server}" >&2 \
        || echo "[claudebox] warning: multica login failed" >&2
fi

# SPIFFE: when the SPIRE agent socket is mounted, fetch this container's X.509 SVID into
# ~/.spiffe (svid.0.pem, svid.0.key, bundle.0.pem) and keep it fresh in the background.
SPIRE_SOCKET="${SPIFFE_ENDPOINT_SOCKET#unix://}"
if [ -S "$SPIRE_SOCKET" ]; then
    fetch_svid() {
        spire-agent api fetch x509 -socketPath "$SPIRE_SOCKET" -timeout 10s -write "$HOME/.spiffe" 2>&1 \
            | awk '/^SPIFFE ID:/{print $3; exit}'
    }
    SPIFFE_ID="$(fetch_svid || true)"
    if [ -n "$SPIFFE_ID" ]; then
        export SPIFFE_ID
        echo "$SPIFFE_ID" > "$HOME/.spiffe/id"
        echo "[claudebox] spiffe: $SPIFFE_ID" >&2
        # X.509 SVIDs live 1h by default; refresh every 20 min.
        ( while sleep 1200; do fetch_svid >/dev/null || true; done ) &
    else
        echo "[claudebox] spiffe: no SVID for this container (run spire/bootstrap.sh?)" >&2
    fi
fi

# OpenAPPA is mandatory: install the Claude Code hooks (idempotent) and run every Claude
# session through `clappa`, which turns the hooks on. If that fails, refuse to start rather
# than run Claude unprotected.
appa_install() {
    # Services share the policy dir, so installs started together wait on its lock.
    for _ in $(seq 20); do
        appa plugin install claude-code --no-agent-yell >"$APPA_DATA_DIR/install.log" 2>&1 && return 0
        grep -q "another installer owns" "$APPA_DATA_DIR/install.log" || return 1
        sleep 3
    done
    return 1
}
# Start from the image's policy (OpenAPPA's default with the organisation's rules
# first, from appa/workspace.toml). A policy that exists stays as it is.
if [ ! -f "$APPA_CONFIG_DIR/appa.toml" ]; then
    cp -R /opt/claudebox/appa/starting-policy/. "$APPA_CONFIG_DIR/"
fi
if ! appa_install || [ ! -x "$APPA_INSTALL_DIR/clappa" ]; then
    echo "[claudebox] error: openappa install failed; refusing to run Claude unprotected." >&2
    cat "$APPA_DATA_DIR/install.log" >&2 2>/dev/null || true
    exit 1
fi
export CLAUDE_BIN="$APPA_INSTALL_DIR/clappa"
export CLAUDEBOX_REAL_CLAUDE="$CLAUDE_BIN"
# On a workspace laptop the daemon starts runs through the run wrapper, which
# hands them to the launcher (CLAUDEBOX_CLAUDE_PATH=/usr/local/bin/claudebox-run).
export MULTICA_CLAUDE_PATH="${CLAUDEBOX_CLAUDE_PATH:-$CLAUDE_BIN}"
echo "[claudebox] openappa: protected sessions via clappa (policy: $APPA_CONFIG_DIR/appa.toml)" >&2

# Always run Claude with permission prompts disabled (the container is the sandbox).
# Set CLAUDEBOX_SKIP_PERMISSIONS=0 to get prompts back.
CLAUDE_ARGS=()
if [ "${CLAUDEBOX_SKIP_PERMISSIONS:-1}" != "0" ]; then
    CLAUDE_ARGS+=(--dangerously-skip-permissions)
fi

# `server [--openai]` starts the HTTP API (see server.js). OPENAI_COMPAT=1 enables /v1/* routes.
if [ "${1:-}" = "server" ]; then
    shift
    while [ $# -gt 0 ]; do
        case "$1" in
            --openai) export OPENAI_COMPAT=1 ;;
            *) echo "[claudebox] unknown server flag: $1" >&2; exit 2 ;;
        esac
        shift
    done
    echo "[claudebox] starting API server on :${PORT:-3000} (openai=${OPENAI_COMPAT:-0}, max_concurrent=${MAX_CONCURRENT:-4})" >&2
    exec node /opt/claudebox/server.js
fi

# Multica is optional; the daemon can't do anything without a login, so say so plainly.
if [ "${1:-}" = "multica" ] && [ "${2:-}" = "daemon" ] && ! grep -q '"token"' "$HOME/.multica/config.json" 2>/dev/null; then
    echo "[claudebox] error: multica-daemon needs MULTICA_TOKEN in .env (a mul_... personal access token)" >&2
    exit 2
fi

# If the first arg looks like a flag or nothing was given, run claude; else run the given command.
if [ $# -eq 0 ] || [ "${1#-}" != "$1" ]; then
    exec "$CLAUDE_BIN" "${CLAUDE_ARGS[@]}" "$@"
fi
exec "$@"
