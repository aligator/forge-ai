# forge-ai

`forge-ai` listens for Forgejo webhooks, starts an AI coding agent for labelled issues or pull requests, pushes the resulting branch, comments back on the ticket, and creates a pull request for issue-triggered work.

## Flow

1. Add the trigger label, default `ai`, to an issue or pull request.
2. Forgejo sends the `issues` or `pull_request` webhook to `POST /webhook`.
3. The service clones or reuses the repository workspace.
4. It checks out the pull request head branch, an existing issue branch, or a new issue branch from the base branch.
5. It runs the configured agent with the ticket context.
6. It commits remaining changes if the agent did not already commit them.
7. It pushes the branch, comments with the last agent output, and creates a pull request for issue work.

Issue branch format:

```text
forge-ai/<owner>/<repo>/<issue-or-pr>-<number>
```

## Local Forgejo

Terminal 1:

```bash
docker compose up
```

Terminal 2:

```bash
go run .
```

`docker compose up` starts Forgejo, creates an admin user, creates `forge-ai/demo`, creates one demo issue, and installs a webhook that points back to your host at `host.lima.internal:8080`. `go run .` starts `forge-ai` locally and creates its own dev token from the bootstrap login.

The dev Compose setup uses the normal Forgejo image, pinned to `codeberg.org/forgejo/forgejo:15`.

Login:

```text
URL:      http://localhost:3000
User:     forge-user
Password: user-password
```

The automation/admin account is `forge-ai / forge-ai-password`.

The default local agent is `codex exec --sandbox workspace-write`. To trigger a run, comment with `@forge-ai` on an issue or pull request.

For pull requests, use a normal conversation comment. Forgejo currently sends inline diff review submissions as `pull_request_comment/reviewed`, but the dev instance may deliver that payload with an empty `review.content`, so there is no mention text for `forge-ai` to read.

To reset the local test instance:

```bash
docker compose down
```

No Forgejo data volume or host bootstrap directory is used; after `docker compose down`, the Forgejo state is gone.

## Agent configuration

For host dev, the agent runs on your machine, not inside Docker. That means normal subscription auth works as-is:

```bash
codex login
# or: claude, then /login
# or: opencode, then /connect
```

Use another CLI by exporting env vars before `go run .`. Leave `AGENT_COMMAND` empty so the service appends the ticket prompt as the final CLI argument:

```bash
AGENT_BIN=claude AGENT_ARGS="--dangerously-skip-permissions --allowedTools Bash,Read,Write,Edit,MultiEdit,Glob,Grep -p" go run .
AGENT_BIN=opencode AGENT_ARGS=run go run .
```

Git use inside the spawned agent is controlled by the prompt policy and the CLI args:

```bash
AGENT_ALLOW_GIT=false go run . # prompt: agent edits files only; forge-ai commits and pushes

AGENT_ALLOW_GIT=true \
  AGENT_ARGS="exec --sandbox danger-full-access" \
  go run .
```

When `AGENT_ALLOW_GIT=true`, the prompt still tells the agent to stay on the prepared branch and only use git status, diff, add, and commit. It must not switch branches or push. The sandbox is just part of `AGENT_ARGS`; use `danger-full-access` if Codex should be able to write `.git`.

Use `AGENT_COMMAND` only for custom shell wrappers; the service exposes the prompt there as `FORGE_AI_PROMPT`.

API keys are still possible, but they are not configured by default. For Claude specifically, do not set `ANTHROPIC_API_KEY` when you want subscription auth; Claude Code gives the API key precedence over subscription OAuth.

## Forgejo MCP

There are existing Forgejo/Gitea MCP servers, including `goern/forgejo-mcp`, `raohwork/forgejo-mcp`, and `SquareCows/forgejo-mcp`. They are useful for assistant-side repository operations, but this service uses Forgejo webhooks and the REST API directly because the automation needs deterministic server-side behavior without requiring the spawned agent to have MCP configured.

## Required environment

```text
FORGEJO_URL=http://localhost:3000
FORGEJO_TOKEN=<optional token with repo read/write and issue/pr access>
FORGEJO_BOOTSTRAP_TOKEN=true
FORGEJO_BOOTSTRAP_USER=forge-ai
FORGEJO_BOOTSTRAP_PASSWORD=forge-ai-password
CLONE_URL_BASE=http://localhost:3000
WEBHOOK_SECRET=<optional>
TRIGGER_MENTION=@forge-ai
WORKSPACE_DIR=.forge-ai/workspaces
BRANCH_PREFIX=forge-ai
CREATE_PR=true
MAX_CONCURRENT=1
AGENT_TIMEOUT=30m
```
