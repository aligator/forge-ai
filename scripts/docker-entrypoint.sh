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

export PATH="/usr/local/bin:/home/agent/.nix-profile/bin:/home/agent/.local/bin:$PATH"
if [ -x /home/agent/.nix-profile/bin/nix ]; then
cat >/usr/local/bin/nix <<'EOF'
#!/bin/sh
if [ "$1" = "develop" ] && [ "${NIX_WRITE_LOCK:-}" != "1" ]; then
    shift
    set -- develop --no-write-lock-file "$@"
fi
if [ "${NIX_VERBOSE:-}" = "1" ]; then
    exec /home/agent/.nix-profile/bin/nix "$@"
fi
exec /home/agent/.nix-profile/bin/nix --quiet "$@"
EOF
    chmod +x /usr/local/bin/nix
fi

exec gosu agent "$@"
