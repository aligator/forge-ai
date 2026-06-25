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
        build-essential procps file ruby \
    && npm install -g @openai/codex @anthropic-ai/claude-code opencode-ai \
    && npm cache clean --force \
    && rm -rf /var/lib/apt/lists/*

# forge-ai binary — root-owned; non-root users cannot delete or overwrite it
COPY --from=build /out/forge-ai /usr/local/bin/forge-ai
COPY scripts/forge-ai-mock-agent /usr/local/bin/forge-ai-mock-agent
RUN chmod +x /usr/local/bin/forge-ai-mock-agent

ARG AGENT_UID=1000
ARG AGENT_GID=1000

# agent user owns workspaces and AI CLI config
RUN set -e; \
    getent group ${AGENT_GID} >/dev/null 2>&1 || groupadd --gid ${AGENT_GID} agent; \
    id agent >/dev/null 2>&1 || useradd --create-home --uid ${AGENT_UID} --gid ${AGENT_GID} --non-unique --shell /bin/bash agent; \
    mkdir -p /var/lib/forge-ai/workspaces /home/agent/.codex /home/agent/.claude /home/agent/.config/opencode; \
    chown -R ${AGENT_UID}:${AGENT_GID} /var/lib/forge-ai /home/agent

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

WORKDIR /var/lib/forge-ai
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/forge-ai"]
