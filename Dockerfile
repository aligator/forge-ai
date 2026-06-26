FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-ai .

FROM node:24-bookworm

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git openssh-client bash curl \
        build-essential procps file ruby gosu \
    && npm install -g @openai/codex @anthropic-ai/claude-code opencode-ai @playwright/mcp \
    && npx playwright install-deps chromium \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/forge-ai /usr/local/bin/forge-ai
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
    mkdir -p /var/lib/forge-ai/workspaces /home/agent/.codex /home/agent/.claude /home/agent/.config/opencode; \
    chown -R agent:agent /var/lib/forge-ai /home/agent

USER agent
ENV HOMEBREW_PREFIX=/home/agent/.homebrew
ENV HOMEBREW_CELLAR=/home/agent/.homebrew/Cellar
ENV HOMEBREW_REPOSITORY=/home/agent/.homebrew
ENV PATH="/home/agent/.homebrew/bin:/home/agent/.homebrew/sbin:$PATH"
RUN set -e; \
    mkdir -p /home/agent/.homebrew; \
    git clone --depth=1 https://github.com/Homebrew/brew /home/agent/.homebrew; \
    /home/agent/.homebrew/bin/brew update --force --quiet; \
    /home/agent/.homebrew/bin/brew --version

COPY scripts/agent-config/claude.json      /home/agent/.claude.json
COPY scripts/agent-config/claude-settings.json /home/agent/.claude/settings.json
COPY scripts/agent-config/codex.toml       /home/agent/.codex/config.toml
COPY scripts/agent-config/opencode.json    /home/agent/.config/opencode/config.json
RUN "$(npm root -g)/@playwright/mcp/node_modules/.bin/playwright" install chromium

USER root
WORKDIR /var/lib/forge-ai
EXPOSE 8080

ENV AGENT_TOOL_HINTS="- Homebrew is installed at ~/.homebrew. Use it to install any CLI tools you need (e.g. brew install ripgrep). Homebrew may compile packages from source which can take several minutes — always let brew installs run to completion, never cancel or interrupt them.\n- Playwright MCP is available for browser automation and web scraping (headless Chromium)."

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["/usr/local/bin/forge-ai"]
