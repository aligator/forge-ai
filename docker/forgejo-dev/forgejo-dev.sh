#!/bin/sh
set -eu

: "${FORGEJO_URL:=http://localhost:3000}"
: "${FORGEJO_INTERNAL_URL:=http://localhost:3000}"
: "${FORGEJO_WORK_PATH:=/data/gitea}"
: "${FORGEJO_CONFIG:=/data/gitea/conf/app.ini}"
: "${FORGEJO_BOOTSTRAP_USER:=forge-ai}"
: "${FORGEJO_BOOTSTRAP_PASSWORD:=forge-ai-password}"
: "${FORGEJO_BOOTSTRAP_EMAIL:=forge-ai@example.invalid}"
: "${FORGEJO_DEV_USER:=forge-user}"
: "${FORGEJO_DEV_PASSWORD:=user-password}"
: "${FORGEJO_DEV_EMAIL:=forge-user@example.invalid}"
: "${FORGEJO_BOOTSTRAP_REPO:=demo}"
: "${FORGEJO_AGENT_USERS:=}"
: "${FORGEJO_AGENT_PASSWORD:=agent-password}"
: "${TRIGGER_LABEL:=ai}"
: "${FORGEJO_BOOTSTRAP_ISSUE:=true}"
: "${WEBHOOK_TARGET_URL:=http://host.lima.internal:8080/webhook}"
: "${WEBHOOK_SECRET:=}"

/usr/bin/entrypoint "$@" &
forgejo_pid="$!"

cleanup() {
	kill "$forgejo_pid" >/dev/null 2>&1 || true
}
trap cleanup INT TERM

run_forgejo() {
	su-exec git forgejo --config "$FORGEJO_CONFIG" --work-path "$FORGEJO_WORK_PATH" "$@"
}

ensure_user() {
	username="$1"
	if ! run_forgejo admin user list | awk 'NR > 1 {print $2}' | grep -Fx "$username" >/dev/null; then
		return 1
	fi
}

until curl -fsS "${FORGEJO_INTERNAL_URL}/api/healthz" >/dev/null 2>&1; do
	echo "waiting for Forgejo at ${FORGEJO_INTERNAL_URL}"
	sleep 2
done

run_forgejo admin user create \
	--admin \
	--username "$FORGEJO_BOOTSTRAP_USER" \
	--password "$FORGEJO_BOOTSTRAP_PASSWORD" \
	--email "$FORGEJO_BOOTSTRAP_EMAIL" \
	--must-change-password=false >/tmp/forgejo-user-create.log 2>&1 || true
if ! ensure_user "$FORGEJO_BOOTSTRAP_USER"; then
	cat /tmp/forgejo-user-create.log >&2
	exit 1
fi

run_forgejo admin user create \
	--admin \
	--username "$FORGEJO_DEV_USER" \
	--password "$FORGEJO_DEV_PASSWORD" \
	--email "$FORGEJO_DEV_EMAIL" \
	--must-change-password=false >/tmp/forgejo-dev-user-create.log 2>&1 || true
if ! ensure_user "$FORGEJO_DEV_USER"; then
	cat /tmp/forgejo-dev-user-create.log >&2
	exit 1
fi

if [ -n "$FORGEJO_AGENT_USERS" ]; then
	echo "$FORGEJO_AGENT_USERS" | tr ',' '\n' | while read -r agent_user; do
		agent_user="$(echo "$agent_user" | tr -d '[:space:]')"
		[ -z "$agent_user" ] && continue
		run_forgejo admin user create \
			--username "$agent_user" \
			--password "$FORGEJO_AGENT_PASSWORD" \
			--email "${agent_user}@example.invalid" \
			--must-change-password=false >/dev/null 2>&1 || true
		if ensure_user "$agent_user"; then
			echo "Agent user ready: ${agent_user}"
		else
			echo "Warning: could not create agent user: ${agent_user}" >&2
		fi
	done
fi

token="$(
	run_forgejo admin user generate-access-token \
		--username "$FORGEJO_BOOTSTRAP_USER" \
		--token-name "forge-ai-bootstrap-$(date +%s)" \
		--scopes all \
		--raw
)"
auth_header="Authorization: Bearer ${token}"
api="${FORGEJO_INTERNAL_URL}/api/v1"

seed_file_if_missing() {
	file_path="$1"
	commit_message="$2"
	file_content="$3"
	file_status="$(curl -fsS -o /dev/null -w '%{http_code}' -H "$auth_header" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/contents/${file_path}" || true)"
	if [ "$file_status" != "200" ]; then
		content="$(printf '%s' "$file_content" | base64 | tr -d '\n')"
		curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
			-d "$(jq -n --arg message "$commit_message" --arg content "$content" '{message:$message, content:$content, branch:"main"}')" \
			"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/contents/${file_path}" >/dev/null
	fi
}

add_repo_collaborator() {
	collaborator="$1"
	[ -z "$collaborator" ] && return 0
	[ "$collaborator" = "$FORGEJO_BOOTSTRAP_USER" ] && return 0
	curl -fsS -X PUT -H "$auth_header" -H "Content-Type: application/json" \
		-d "$(jq -n '{permission:"write"}')" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/collaborators/${collaborator}" >/dev/null
}

status="$(curl -fsS -o /dev/null -w '%{http_code}' -H "$auth_header" \
	"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}" || true)"
if [ "$status" != "200" ]; then
	curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
		-d "$(jq -n --arg name "$FORGEJO_BOOTSTRAP_REPO" '{name:$name, private:false, auto_init:true, default_branch:"main"}')" \
		"${api}/user/repos" >/dev/null
fi

add_repo_collaborator "$FORGEJO_DEV_USER"
if [ -n "$FORGEJO_AGENT_USERS" ]; then
	echo "$FORGEJO_AGENT_USERS" | tr ',' '\n' | while read -r agent_user; do
		agent_user="$(echo "$agent_user" | tr -d '[:space:]')"
		add_repo_collaborator "$agent_user"
	done
fi

seed_file_if_missing "README.md" "seed demo README" "$(cat <<'EOF'
# forge-ai demo

This repository is seeded by docker compose for local testing.

It includes a Go-oriented Nix flake and `.forge-ai/instructions.md` so agents have repo-specific guidance.
EOF
)"

seed_file_if_missing "flake.nix" "seed demo Nix flake" "$(cat <<'EOF'
{
  description = "forge-ai demo Go development shell";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { nixpkgs, ... }:
    let
      systems = [
        "aarch64-darwin"
        "aarch64-linux"
        "x86_64-darwin"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
    in
    {
      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go
              gopls
              gotools
              gofumpt
              golangci-lint
            ];

            shellHook = ''
              echo "Go $(go version)"
              echo "Run: go test ./..."
            '';
          };
        });
    };
}
EOF
)"

seed_file_if_missing ".forge-ai/instructions.md" "seed forge-ai instructions" "$(cat <<'EOF'
# forge-ai instructions

Read this file before changing code.

- Use the Nix dev shell when Go tooling is needed: `nix develop`.
- Keep changes small and focused on the ticket.
- Follow existing Go style.
- Format changed Go files with `gofmt` or `gofumpt`.
- Run `go test ./...` before finishing when practical.
- Do not commit, push, rebase, reset, or switch branches unless forge-ai explicitly allows git for this run.
- Write the final commit message to `.forge-ai-commit-msg`.
EOF
)"

curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
	-d "$(jq -n --arg name "$TRIGGER_LABEL" '{name:$name, color:"#0e8a16", description:"Run forge-ai"}')" \
	"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/labels" >/dev/null 2>&1 || true

label_id="$(
	curl -fsS \
		-H "$auth_header" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/labels" \
		| jq -r --arg name "$TRIGGER_LABEL" '.[] | select(.name == $name) | .id' \
		| head -1
)"

hooks="$(curl -fsS -H "$auth_header" "${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/hooks")"
hook_events='["issues","issue_comment","pull_request","pull_request_comment"]'
hook_id="$(printf '%s' "$hooks" | jq -r --arg url "$WEBHOOK_TARGET_URL" '.[] | select(.config.url == $url) | .id' | head -1)"
if [ -n "$hook_id" ] && [ "$hook_id" != "null" ]; then
	hook_payload="$(jq -n --arg url "$WEBHOOK_TARGET_URL" --arg secret "$WEBHOOK_SECRET" --argjson events "$hook_events" \
		'{config:{url:$url, content_type:"json", secret:$secret}, events:$events, active:true}')"
	curl -fsS -X PATCH -H "$auth_header" -H "Content-Type: application/json" \
		-d "$hook_payload" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/hooks/${hook_id}" >/dev/null
else
	hook_payload="$(jq -n --arg url "$WEBHOOK_TARGET_URL" --arg secret "$WEBHOOK_SECRET" --arg type "forgejo" \
		--argjson events "$hook_events" \
		'{type:$type, config:{url:$url, content_type:"json", secret:$secret}, events:$events, active:true}')"
	curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
		-d "$hook_payload" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/hooks" >/dev/null || {
		hook_payload="$(jq -n --arg url "$WEBHOOK_TARGET_URL" --arg secret "$WEBHOOK_SECRET" --arg type "gitea" \
			--argjson events "$hook_events" \
			'{type:$type, config:{url:$url, content_type:"json", secret:$secret}, events:$events, active:true}')"
		curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
			-d "$hook_payload" \
			"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/hooks" >/dev/null
	}
fi

if [ "$FORGEJO_BOOTSTRAP_ISSUE" = "true" ] && [ -n "$label_id" ]; then
	issues="$(
		curl -fsS -H "$auth_header" \
			"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/issues?state=all"
	)"
	if ! printf '%s' "$issues" | jq -e '.[] | select(.title == "Demo: run forge-ai")' >/dev/null; then
		curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
			-d "$(jq -n --argjson label_id "$label_id" '{
				title:"Demo: run forge-ai",
				body:"This issue is created by the Forgejo dev container. Start `go run .`, then add or re-add the `ai` label to trigger forge-ai.",
				labels:[$label_id]
			}')" \
			"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/issues" >/dev/null
	fi
fi

cat <<EOF
Forgejo dev instance is ready.
URL:        ${FORGEJO_URL}
Bot user:   ${FORGEJO_BOOTSTRAP_USER} / ${FORGEJO_BOOTSTRAP_PASSWORD}
Dev user:   ${FORGEJO_DEV_USER} / ${FORGEJO_DEV_PASSWORD}
Repo:       ${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}
Issue:      Demo: run forge-ai
Webhook:    ${WEBHOOK_TARGET_URL}
Agents:     ${FORGEJO_AGENT_USERS:-none} (password: ${FORGEJO_AGENT_PASSWORD})
EOF

wait "$forgejo_pid"
