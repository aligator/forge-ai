FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-ai .

FROM node:24-bookworm

# System deps — build-essential and file required by Homebrew
RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        ca-certificates git openssh-client bash curl \
        build-essential procps file ruby gosu \
    && npm install -g @openai/codex @anthropic-ai/claude-code opencode-ai \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/*

# forge-ai binary — root-owned; non-root users cannot delete or overwrite it
COPY --from=build /out/forge-ai /usr/local/bin/forge-ai
COPY scripts/forge-ai-mock-agent /usr/local/bin/forge-ai-mock-agent
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/forge-ai-mock-agent /usr/local/bin/docker-entrypoint.sh

# Create agent user with fixed UID/GID 1000; runtime remapping handled by entrypoint
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

# Install Homebrew into agent home — agent can brew install freely, cannot touch /usr/local/bin
# Use git-clone method (supported alternative install) so HOMEBREW_PREFIX is respected
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

USER root
WORKDIR /var/lib/forge-ai
EXPOSE 8080
ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["/usr/local/bin/forge-ai"]
