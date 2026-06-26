ARG BINARY_PROVIDER=build

FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /forge-ai .

FROM scratch AS prebuilt
ARG TARGETARCH
COPY linux/${TARGETARCH}/forge-ai /forge-ai

FROM ${BINARY_PROVIDER} AS binary-provider

FROM node:24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git openssh-client bash curl \
        build-essential procps file ruby gosu \
    && npm install -g @openai/codex @anthropic-ai/claude-code opencode-ai @playwright/mcp \
    && npx playwright install-deps chromium \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/*

COPY --from=binary-provider /forge-ai /usr/local/bin/forge-ai
COPY scripts/forge-ai-mock-agent /usr/local/bin/forge-ai-mock-agent
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/forge-ai-mock-agent /usr/local/bin/docker-entrypoint.sh

RUN set -e; \
    if getent group 1000 >/dev/null 2>&1; then \
        groupmod -n agent "$(getent group 1000 | cut -d: -f1)"; \
    else \
        groupadd --gid 1000 agent; \
    fi; \
    if id -u 1000 >/dev/null 2>&1; then \
        usermod -l agent -d /home/agent -m -s /bin/bash "$(id -nu 1000)"; \
    else \
        useradd --create-home --uid 1000 --gid 1000 --shell /bin/bash agent; \
    fi; \
    mkdir -p /nix /var/lib/forge-ai/workspaces /home/agent/.codex /home/agent/.claude /home/agent/.config/opencode; \
    chown -R agent:agent /nix /var/lib/forge-ai /home/agent

USER agent
ENV PATH="/home/agent/.nix-profile/bin:/home/agent/.local/bin:$PATH"
RUN curl -sSL https://nixos.org/nix/install | sh -s -- --no-daemon \
    && . /home/agent/.nix-profile/etc/profile.d/nix.sh \
    && nix-channel --update

COPY scripts/agent-config/claude.json      /home/agent/.claude.json
COPY scripts/agent-config/claude-settings.json /home/agent/.claude/settings.json
COPY scripts/agent-config/codex.toml       /home/agent/.codex/config.toml
COPY scripts/agent-config/opencode.json    /home/agent/.config/opencode/config.json
RUN "$(npm root -g)/@playwright/mcp/node_modules/.bin/playwright" install chromium

USER root
WORKDIR /var/lib/forge-ai
EXPOSE 8080

ENV AGENT_TOOL_HINTS="- Nix is installed (single-user). Use it to install any CLI tools you need without root: nix-env -iA nixpkgs.ripgrep (prebuilt binaries, fast). Run . ~/.nix-profile/etc/profile.d/nix.sh first if nix commands are not found.\n- Playwright MCP is available for browser automation and web scraping (headless Chromium)."

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["/usr/local/bin/forge-ai"]
