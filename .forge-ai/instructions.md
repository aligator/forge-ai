# Forge AI Instructions

## Rules

- Do only the issue/review task. Read the full issue/PR context and latest comments first.
- Use `rtk` for shell commands when available.
- Committing and pushing is not the default flow: `forge-ai` commits and pushes after the agent exits. Run git yourself only when a workflow needs it (e.g. staging new files so a Nix flake sees them, or merging the base branch).
- Stay on the prepared branch. Do not switch branches or rewrite existing history.
- Do not modify credentials, auth files, `.env`, or host-mounted secrets.
- Do not edit generated/build output in `dist/` unless the task explicitly requires release artifacts.
- Keep changes minimal and focused.

## Style

- Do not write one-line `if` statements. Always use braces with the condition, body, and closing brace on separate lines:

```go
if condition {
	doThing()
}
```

## Validation

Prefer targeted checks:

```bash
rtk go test ./internal/agent ./internal/service
```

For broader validation:

```bash
rtk go test ./...
```

If the sandbox or container has a read-only home Go cache:

```bash
GOCACHE=/tmp/forge-ai-go-build rtk go test ./...
```

## App startup

For local Forgejo webhook testing in a Forge AI container, use the prepared Nix shell:

```bash
nix develop
```

Then, only if runtime testing is required by the task:

```bash
rtk docker compose --profile host up
rtk go run .
```

Do not start long-lived services unless the task needs them. Stop services before finishing.

## Agent CLIs

The dev shell can install the same agent CLIs used by the container:

```bash
forge-ai-agent-npm-tools
```

For Playwright MCP browser support:

```bash
npx playwright install chromium
```

For the Caveman skill/plugin from `JuliusBrussee/caveman`, use caveman style by default:

- Terse, direct, no filler or pleasantries.
- Preserve exact code, commands, file paths, API names, and error output.
- Use normal clarity for destructive actions, security warnings, or complex sequences where compression could mislead.
- If the agent supports Caveman commands, `/caveman full` is the default; use `/caveman lite` for clearer prose and `/caveman ultra` for maximum compression.

## Nix

Use the prepared dev shell:

```bash
nix develop
```

It contains Go, Node.js, git, ripgrep, jq, curl, Ruby, OpenSSH, and build tools. It keeps Go/npm/Playwright caches under the repo by default.
