# syntax=docker/dockerfile:1.7
#
# Release image. All heavy, rarely-changing layers live in Dockerfile.base
# (published as forge-ai-base). This file only compiles/copies the forge-ai
# binary on top, so a release image build is effectively a COPY and stays fast
# even under multi-arch emulation.
#
# BASE_IMAGE must point at a forge-ai-base image built from Dockerfile.base.
# BINARY_PROVIDER selects how the forge-ai binary is provided:
#   - "prebuilt": copied from ./linux/${TARGETARCH}/forge-ai (used by goreleaser)
#   - "build":    compiled here (used for standalone local builds)

ARG BASE_IMAGE=ghcr.io/forge-ai/forge-ai-base:latest
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

FROM ${BASE_IMAGE}

COPY --from=binary-provider /forge-ai /usr/local/bin/forge-ai
COPY scripts/forge-ai-mock-agent /usr/local/bin/forge-ai-mock-agent
COPY scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
RUN chmod +x /usr/local/bin/forge-ai-mock-agent /usr/local/bin/docker-entrypoint.sh

ENTRYPOINT ["docker-entrypoint.sh"]
CMD ["/usr/local/bin/forge-ai"]
