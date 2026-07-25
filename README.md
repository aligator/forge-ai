# Forge AI

`forge-ai` listens for Forgejo webhooks, starts an AI coding agent when a configured mention appears on an issue or pull request, pushes the resulting branch, comments back on the ticket, and creates a pull request for issue-triggered work.

## Flow

1. Mention a configured agent, for example `@forge-ai`, `@codex`, `@claude`, or `@opencode`, on an issue or pull request.
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

## Project templates

Projects can customize the final Forgejo comment by adding `.forge-ai/final-message.md` next to `.forge-ai/instructions.md`. The file is rendered as a Go `text/template`; missing or empty files use the built-in final message.

Available template variables include:

```text
{{.RunID}} {{.RunKind}} {{.ParentRunID}} {{.CreatedBy}}
{{.Owner}} {{.Repo}} {{.RepoFullName}} {{.RepositoryURL}}
{{.TicketKind}} {{.TicketNumber}} {{.TicketTitle}} {{.TicketBody}} {{.TicketURL}} {{.IssueURL}}
{{.Branch}} {{.BranchURL}} {{.Base}} {{.Committed}}
{{.AgentMention}} {{.AgentType}} {{.AgentID}} {{.AgentSessionID}}
{{.ForgejoURL}}
{{.PullRequestNumber}} {{.PullRequestURL}} {{.PullRequestText}}
{{.PullRequest.Number}} {{.PullRequest.URL}} {{.PullRequest.HTMLURL}} {{.PullRequest.Text}}
```

## Local Forgejo

Create a local env file:

```bash
cp .env.example .env
```

Terminal 1:

```bash
docker compose --profile host up
```

Terminal 2:

```bash
go run .
```

`docker compose --profile host up` starts a host-dev Forgejo instance, creates an admin user, creates `forge-ai/demo`, creates one demo issue, and installs a webhook that points back to `go run .` on your host via `http://host.docker.internal:8080/webhook`. `go run .` starts `forge-ai` locally and creates its own dev token from the bootstrap login.

The dev Compose setup uses the normal Forgejo image, pinned to `codeberg.org/forgejo/forgejo:16`.

Login:

```text
URL:      http://localhost:3000
User:     forge-user
Password: user-password
```

The automation/admin account is `forge-ai / forge-ai-password`.

The `.env.example` default local agent is a no-credential mock agent. It is useful for webhook and branch-flow smoke tests. To trigger it, comment with `@forge-ai` on an issue or pull request. To use a real CLI, edit `.env` or export `AGENT_0_*` variables before `go run .`.

For pull requests, use a normal conversation comment. Forgejo currently sends inline diff review submissions as `pull_request_comment/reviewed`, but the dev instance may deliver that payload with an empty `review.content`, so there is no mention text for `forge-ai` to read.

To reset the local test instance:

```bash
docker compose down
```

No Forgejo data volume or host bootstrap directory is used; after `docker compose down`, the Forgejo state is gone.

## Production

```bash
FORGEJO_URL=https://forgejo.example.com \
FORGEJO_TOKEN=<token> \
docker compose --profile full up -d
```

The `full` profile builds the forge-ai image. The image includes claude, codex, opencode, Playwright MCP, Forgejo MCP, Caveman skill/plugin support, RTK, and single-user Nix for the `agent` user. It mounts AI CLI credentials from the host:

| Volume | Default host path |
|--------|------------------|
| Claude credentials | `~/.claude/.credentials.json` |
| Codex auth | `~/.codex/auth.json` |

Override with `CLAUDE_CREDENTIALS` and `CODEX_CREDENTIALS`. Workspaces persist in a named Docker volume (`forge-ai-workspaces`).

`FORGEJO_URL` is required. All other variables have defaults matching the dev setup.

## Agent configuration

For host dev, the agent runs on your machine, not inside Docker. That means normal subscription auth works as-is after you configure a real agent:

```bash
codex login
# or: claude, then /login
# or: opencode, then /connect
```

Use another CLI by editing `.env` or exporting env vars before `go run .`. Leave `AGENT_0_COMMAND` empty so the service appends the ticket prompt as the final CLI argument:

```bash
AGENT_0_USER=claude AGENT_0_BIN=claude AGENT_0_ARGS="--dangerously-skip-permissions --allowedTools Bash,Read,Write,Edit,MultiEdit,Glob,Grep -p" go run .
AGENT_0_USER=codex AGENT_0_BIN=codex AGENT_0_ARGS="exec --sandbox workspace-write" go run .
AGENT_0_USER=opencode AGENT_0_BIN=opencode AGENT_0_ARGS=run go run .
```

Git use inside the spawned agent is controlled by the prompt policy and the CLI args:

```bash
AGENT_ALLOW_GIT=false go run . # prompt: agent edits files only; forge-ai commits and pushes

AGENT_ALLOW_GIT=true \
  AGENT_0_ARGS="exec --sandbox danger-full-access" \
  go run .
```

When `AGENT_ALLOW_GIT=true`, the prompt still tells the agent to stay on the prepared branch and only use git status, diff, add, and commit. It must not switch branches or push. The sandbox is just part of `AGENT_0_ARGS`; use `danger-full-access` if Codex should be able to write `.git`.

Commit identity defaults to `GIT_USER_NAME` and `GIT_USER_EMAIL`. Override it per configured agent with `AGENT_0_GIT_USER_NAME` and `AGENT_0_GIT_USER_EMAIL`. The selected identity is written to the worktree before the agent starts, so it applies both to forge-ai's automatic commit and to agent-created git commits.

Use `AGENT_0_COMMAND` only for custom shell wrappers; the service exposes the prompt there as `FORGE_AI_PROMPT`.

API keys are still possible, but they are not configured by default. For Claude specifically, do not set `ANTHROPIC_API_KEY` when you want subscription auth; Claude Code gives the API key precedence over subscription OAuth.

## Caveman skill/plugin

The image and dev shell install Caveman skill/plugin support with each agent's native install path: Claude plugin, Codex skill, and opencode skill. Forge AI also tells spawned agents to use caveman style by default:

- terse, direct, no filler;
- exact code, commands, paths, API names, and errors preserved;
- normal clarity for destructive actions, security warnings, or complex sequences;
- `/caveman full` as default when the agent supports Caveman commands.


## Forgejo MCP

There are existing Forgejo/Gitea MCP servers, including `goern/forgejo-mcp`, `raohwork/forgejo-mcp`, and `SquareCows/forgejo-mcp`. They are useful for assistant-side repository operations, but this service uses Forgejo webhooks and the REST API directly because the automation needs deterministic server-side behavior without requiring the spawned agent to have MCP configured.

## Required environment

```text
FORGEJO_URL=http://localhost:3000
FORGEJO_TOKEN=<optional token with repo read/write and issue/pr access>
FORGEJO_BOOTSTRAP_TOKEN=true
FORGEJO_BOOTSTRAP_USER=forge-ai
FORGEJO_BOOTSTRAP_PASSWORD=forge-ai-password
FORGEJO_BOOTSTRAP_TOKEN_NAME=forge-ai-local
CLONE_URL_BASE=http://localhost:3000
WEBHOOK_SECRET=<optional>
WORKSPACE_DIR=.forge-ai/workspaces
BRANCH_PREFIX=forge-ai
CREATE_PR=true
MAX_CONCURRENT=1
AGENT_ALLOW_GIT=false
GIT_USER_NAME=forge-ai
GIT_USER_EMAIL=forge-ai@example.invalid
AGENT_0_USER=forge-ai
AGENT_0_BIN=<agent executable>
AGENT_0_ARGS=<optional args>
AGENT_0_COMMAND=<optional shell wrapper>
AGENT_0_TIMEOUT=30m
AGENT_0_PASSWORD=<optional Forgejo password for per-agent token bootstrap>
AGENT_0_TOKEN=<optional per-agent Forgejo token>
AGENT_0_TOKEN_FILE=<optional per-agent Forgejo token file>
AGENT_0_GIT_USER_NAME=<optional agent override>
AGENT_0_GIT_USER_EMAIL=<optional agent override>
```
