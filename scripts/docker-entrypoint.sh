#!/bin/bash
set -e

AGENT_UID=${AGENT_UID:-1000}
AGENT_GID=${AGENT_GID:-1000}

CURRENT_GID=$(id -g agent)
CURRENT_UID=$(id -u agent)

if [ "$AGENT_GID" != "$CURRENT_GID" ]; then
    groupmod -g "$AGENT_GID" agent
fi

if [ "$AGENT_UID" != "$CURRENT_UID" ]; then
    usermod -u "$AGENT_UID" agent
fi

if [ "$AGENT_UID" != "$CURRENT_UID" ] || [ "$AGENT_GID" != "$CURRENT_GID" ]; then
    chown -R agent:agent /var/lib/forge-ai /home/agent
fi

exec gosu agent "$@"
