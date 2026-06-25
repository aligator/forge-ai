FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/forge-ai .

FROM node:22-bookworm-slim
RUN apt-get update \
	&& apt-get install -y --no-install-recommends ca-certificates git openssh-client bash curl \
	&& npm install -g @openai/codex @anthropic-ai/claude-code opencode-ai \
	&& npm cache clean --force \
	&& rm -rf /var/lib/apt/lists/*
COPY --from=build /out/forge-ai /usr/local/bin/forge-ai
COPY scripts/forge-ai-mock-agent /usr/local/bin/forge-ai-mock-agent
RUN chmod +x /usr/local/bin/forge-ai-mock-agent \
	&& useradd --create-home --home-dir /var/lib/forge-ai --shell /bin/bash forge-ai \
	&& mkdir -p /var/lib/forge-ai/.codex /var/lib/forge-ai/.claude /var/lib/forge-ai/.config/opencode /var/lib/forge-ai/workspaces \
	&& chown -R forge-ai:forge-ai /var/lib/forge-ai
USER forge-ai
WORKDIR /var/lib/forge-ai
EXPOSE 8080
ENTRYPOINT ["forge-ai"]
