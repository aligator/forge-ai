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

token="$(
	run_forgejo admin user generate-access-token \
		--username "$FORGEJO_BOOTSTRAP_USER" \
		--token-name "forge-ai-bootstrap-$(date +%s)" \
		--scopes all \
		--raw
)"
auth_header="Authorization: Bearer ${token}"
api="${FORGEJO_INTERNAL_URL}/api/v1"

status="$(curl -fsS -o /dev/null -w '%{http_code}' -H "$auth_header" \
	"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}" || true)"
if [ "$status" != "200" ]; then
	curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
		-d "$(jq -n --arg name "$FORGEJO_BOOTSTRAP_REPO" '{name:$name, private:false, auto_init:true, default_branch:"main"}')" \
		"${api}/user/repos" >/dev/null
fi

readme_status="$(curl -fsS -o /dev/null -w '%{http_code}' -H "$auth_header" \
	"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/contents/README.md" || true)"
if [ "$readme_status" != "200" ]; then
	content="$(printf '# forge-ai demo\n\nThis repository is seeded by docker compose for local testing.\n' | base64 | tr -d '\n')"
	curl -fsS -X POST -H "$auth_header" -H "Content-Type: application/json" \
		-d "$(jq -n --arg content "$content" '{message:"seed demo README", content:$content, branch:"main"}')" \
		"${api}/repos/${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}/contents/README.md" >/dev/null
fi

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
URL:      ${FORGEJO_URL}
User:     ${FORGEJO_BOOTSTRAP_USER}
Password: ${FORGEJO_BOOTSTRAP_PASSWORD}
Dev user: ${FORGEJO_DEV_USER}
Dev pass: ${FORGEJO_DEV_PASSWORD}
Repo:     ${FORGEJO_BOOTSTRAP_USER}/${FORGEJO_BOOTSTRAP_REPO}
Issue:    Demo: run forge-ai
Webhook:  ${WEBHOOK_TARGET_URL}
EOF

wait "$forgejo_pid"
