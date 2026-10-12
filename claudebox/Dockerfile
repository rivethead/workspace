# syntax=docker/dockerfile:1
#
# claudebox - a self-contained image for running Claude Code (Anthropic's CLI).
#
# Build:   docker build -t claudebox .
#          docker build --build-arg CLAUDE_CODE_VERSION=2.1.267 -t claudebox .
# Run:     docker compose run --rm claudebox            interactive Claude Code
#          docker compose up -d claudebox-server       OpenAI-compatible API on :3000
#
# Node is included because many MCP servers and project toolchains need `npx`/`npm`.
ARG SPIRE_VERSION=1.15.3
FROM ghcr.io/spiffe/spire-agent:${SPIRE_VERSION} AS spire

# The run wrapper and launcher (launcher/): every agent run gets a fresh container.
FROM golang:1.26 AS launcher
WORKDIR /src
COPY launcher/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/claudebox-launcher ./cmd/claudebox-launcher

FROM node:22-bookworm-slim

ARG CLAUDE_CODE_VERSION=latest
ARG MULTICA_VERSION=latest
ARG APPA_VERSION=v0.31.1
ARG TARGETARCH
ARG USERNAME=claude
ARG UID=1000
ARG GID=1000
ARG TZ=UTC

ENV TZ=${TZ} \
    DEBIAN_FRONTEND=noninteractive

# Base tooling Claude Code and typical dev workflows expect.
RUN apt-get update && apt-get install -y --no-install-recommends \
        ca-certificates curl git openssh-client gnupg \
        ripgrep jq less procps unzip zip nano \
        python3 python3-pip python3-venv \
    && rm -rf /var/lib/apt/lists/*

# Install a pinned Claude Code native binary from the official release bucket.
# "latest" resolves to the current stable version at build time.
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
        amd64) PLATFORM="linux-x64" ;; \
        arm64) PLATFORM="linux-arm64" ;; \
        *) echo "unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac; \
    BUCKET="https://downloads.claude.ai/claude-code-releases"; \
    if [ "${CLAUDE_CODE_VERSION}" = "latest" ]; then \
        VERSION="$(curl -fsSL "${BUCKET}/latest")"; \
    else \
        VERSION="${CLAUDE_CODE_VERSION}"; \
    fi; \
    curl -fsSL "${BUCKET}/${VERSION}/${PLATFORM}/claude" -o /usr/local/bin/claude; \
    chmod +x /usr/local/bin/claude; \
    echo "${VERSION}" > /etc/claude-code-version; \
    /usr/local/bin/claude --version

# Multica CLI, so the box can run a Multica agent-runtime daemon (`multica daemon start`).
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
        amd64|arm64) ARCH="${TARGETARCH:-amd64}" ;; \
        *) echo "unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac; \
    if [ "${MULTICA_VERSION}" = "latest" ]; then \
        VERSION="$(curl -fsSLI -o /dev/null -w '%{url_effective}' https://github.com/multica-ai/multica/releases/latest | sed 's|.*/tag/v||')"; \
    else \
        VERSION="${MULTICA_VERSION#v}"; \
    fi; \
    curl -fsSL "https://github.com/multica-ai/multica/releases/download/v${VERSION}/multica-cli-${VERSION}-linux-${ARCH}.tar.gz" \
        | tar -xz -C /usr/local/bin multica; \
    chmod 755 /usr/local/bin/multica; \
    /usr/local/bin/multica version

# OpenAPPA (`appa`): deterministic tool-call policy enforced through Claude Code hooks.
# The entrypoint runs `appa plugin install claude-code` and launches claude through `clappa`.
RUN set -eux; \
    case "${TARGETARCH:-amd64}" in \
        amd64) TRIPLE="x86_64-unknown-linux-gnu" ;; \
        arm64) TRIPLE="aarch64-unknown-linux-gnu" ;; \
        *) echo "unsupported arch: ${TARGETARCH}" && exit 1 ;; \
    esac; \
    BASE="https://github.com/archestra-ai/OpenAPPA/releases/download/${APPA_VERSION}"; \
    cd /tmp; \
    curl -fsSLO "${BASE}/appa-${TRIPLE}.tar.gz"; \
    curl -fsSL "${BASE}/SHA256SUMS" | grep " appa-${TRIPLE}.tar.gz\$" | sha256sum -c -; \
    tar -xzf "appa-${TRIPLE}.tar.gz" -C /usr/local/bin ./appa; \
    rm "appa-${TRIPLE}.tar.gz"; \
    chmod 755 /usr/local/bin/appa; \
    appa --version

# SPIRE agent binary, used only as a Workload API client (`spire-agent api fetch ...`).
COPY --from=spire /opt/spire/bin/spire-agent /usr/local/bin/spire-agent

# The run wrapper. With CLAUDEBOX_CLAUDE_PATH=/usr/local/bin/claudebox-run, the
# Multica daemon starts each run through it, and the launcher runs it in a fresh
# container (ARCHITECTURE.md D14).
COPY --from=launcher /out/claudebox-launcher /usr/local/bin/claudebox-launcher
RUN printf '#!/bin/sh\nexec /usr/local/bin/claudebox-launcher run "$@"\n' > /usr/local/bin/claudebox-run \
    && chmod 755 /usr/local/bin/claudebox-run

# Unprivileged user so Claude never runs as root inside the container.
# The node image already owns uid/gid 1000 as "node"; reuse or replace as needed.
RUN set -eux; \
    if getent passwd "${UID}" >/dev/null; then userdel -r "$(getent passwd "${UID}" | cut -d: -f1)"; fi; \
    if getent group "${GID}" >/dev/null; then groupdel "$(getent group "${GID}" | cut -d: -f1)"; fi; \
    groupadd -g "${GID}" "${USERNAME}"; \
    useradd -m -u "${UID}" -g "${GID}" -s /bin/bash "${USERNAME}"; \
    mkdir -p /workspace /home/${USERNAME}/.claude /home/${USERNAME}/.npm-global \
        /home/${USERNAME}/.multica /home/${USERNAME}/multica-workspaces \
        /home/${USERNAME}/.config/appa /home/${USERNAME}/.local/share/appa /home/${USERNAME}/.local/bin \
        /home/${USERNAME}/.spiffe; \
    chown -R "${UID}:${GID}" /workspace /home/${USERNAME}

# Git identity inside the box; safe.directory avoids "dubious ownership" on bind mounts.
RUN git config --system --add safe.directory '*'

# Entrypoint and API server (copied as root, world-readable).
COPY --chmod=755 entrypoint.sh /usr/local/bin/entrypoint.sh
COPY --chmod=644 server.js mcp-bridge.js /opt/claudebox/
RUN chmod 755 /opt/claudebox

# OpenAPPA's hooks as Claude Code managed settings: a settings file in a work
# folder (.claude/settings.json) cannot switch them off, and allowManagedHooksOnly
# runs no other hooks. The hooks come from OpenAPPA's own install, done here as
# the user in a scratch folder, then pointed at APPA_DATA_DIR, which the
# entrypoint's install fills at runtime.
RUN set -eux; \
    probe=/tmp/appa-probe; \
    install -d -o "${UID}" -g "${GID}" "$probe"; \
    runuser -u "${USERNAME}" -- env HOME="$probe" CLAUDE_CONFIG_DIR="$probe/.claude" \
        APPA_CONFIG_DIR="$probe/appa" APPA_INSTALL_DIR="$probe/bin" \
        APPA_DATA_DIR="$probe/data" \
        appa plugin install claude-code --no-agent-yell; \
    mkdir -p /etc/claude-code; \
    node -e 'const s = require(process.argv[1]); \
        if (!s.hooks || !s.hooks.PreToolUse) throw new Error("no OpenAPPA hooks"); \
        const out = JSON.stringify({ hooks: s.hooks, allowManagedHooksOnly: true }, null, 2); \
        process.stdout.write(out.split(process.argv[2]).join(process.argv[3]));' \
        "$probe/.claude/settings.json" "$probe/data" "/home/${USERNAME}/.local/share/appa" \
        > /etc/claude-code/managed-settings.json; \
    ! grep -q "$probe" /etc/claude-code/managed-settings.json; \
    chmod 644 /etc/claude-code/managed-settings.json; \
    # Keep OpenAPPA's default policy for the next step.
    mkdir -p /opt/claudebox/appa/default-policy; \
    cp -R "$probe/appa/appa.toml" "$probe/appa/batteries" /opt/claudebox/appa/default-policy/; \
    rm -rf "$probe"

# OpenAPPA's starting policy: the default with the organisation's rules first. The
# entrypoint copies it into APPA_CONFIG_DIR when that has no policy yet (the
# install keeps a policy that exists, but cannot replace folders in an image
# layer). A separate step, so a change to the rules does not run the install.
COPY --chmod=644 appa/workspace.toml /opt/claudebox/appa/workspace.toml
RUN set -eux; \
    policy=/opt/claudebox/appa/starting-policy; \
    mkdir -p "$policy"; \
    cp -R /opt/claudebox/appa/default-policy/batteries "$policy/"; \
    awk -v rules=/opt/claudebox/appa/workspace.toml \
        '{ print } /^version = 2$/ && !done { print ""; while ((getline l < rules) > 0) print l; done = 1 }' \
        /opt/claudebox/appa/default-policy/appa.toml > "$policy/appa.toml"; \
    grep -q 'Bash(command:multica \*)' "$policy/appa.toml"; \
    chmod -R a+rX /opt/claudebox/appa; \
    # Claude Code saves sessions here. Runs mount each agent's session volume on
    # it, and a new volume takes this folder's owner.
    install -d -o "${UID}" -g "${GID}" "/home/${USERNAME}/.claude/projects"

USER ${USERNAME}
WORKDIR /workspace

ENV HOME=/home/${USERNAME} \
    CLAUDE_CONFIG_DIR=/home/${USERNAME}/.claude \
    NPM_CONFIG_PREFIX=/home/${USERNAME}/.npm-global \
    PATH=/home/${USERNAME}/.npm-global/bin:/home/${USERNAME}/.local/bin:${PATH} \
    # OpenAPPA's default Linux locations. Keep them: the default policy's self-protection
    # rules (editing */appa/appa.toml or */appa/batteries/* needs human approval) match these paths.
    APPA_CONFIG_DIR=/home/${USERNAME}/.config/appa \
    APPA_DATA_DIR=/home/${USERNAME}/.local/share/appa \
    APPA_INSTALL_DIR=/home/${USERNAME}/.local/bin \
    SPIFFE_ENDPOINT_SOCKET=unix:///run/spire/sockets/agent.sock \
    DISABLE_AUTOUPDATER=1 \
    NODE_OPTIONS=--max-old-space-size=4096

# Persist login/config and the project across runs.
VOLUME ["/home/claude/.claude", "/workspace"]

EXPOSE 3000
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
CMD []
