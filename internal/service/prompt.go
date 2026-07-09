package service

import (
	"fmt"
	"strings"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
)

func prompt(ticket forgejo.Ticket, branch, base string, allowGit bool, toolHints string) string {
	gitPolicy := `Repo already on branch. No git cmds. Edit files only; forge-ai commits+pushes.`
	if allowGit {
		gitPolicy = `Repo already on branch. Stay there. Allowed: git status, diff, add, commit. Forbidden: create/switch/reset/rebase/merge/delete branches, push. forge-ai pushes+posts.`
	}
	var toolSection string
	if strings.TrimSpace(toolHints) != "" {
		toolSection = "Tools:\n" + strings.TrimSpace(toolHints) + "\n\n"
	}
	return fmt.Sprintf(`Work in cloned Forgejo repo.

Repo: %s/%s
Ticket: %s #%d
Branch: %s
Base: %s
URL: %s

Forgejo API: use FORGEJO_URL env. No localhost unless FORGEJO_URL says localhost.

Title:
%s

Body:
%s

Trigger comment:
%s

Trigger comment = main task. Body = context. If the user asks clear questions or asks for analysis/advice, answer only; do not implement unless the user asks for code changes. Before plan: read issue/PR comments via Forgejo MCP. API fallback only if MCP fails. Apply relevant later comments. Need to communicate with user? Post a Forgejo comment via Forgejo MCP. You may post short progress updates or questions whenever useful.
%s

%sYou are the best developer. No quick fixes. You do not think in human time — you are an agent and work much faster. First: read .forge-ai/instructions.md. Inspect relevant files only. Skip configs unless needed. Current repo only. No parent/sibling dirs. Read relevant AGENTS.md/CLAUDE.md. Never print secrets/full env. Prefix shell cmds with 'rtk'. Keep changes minimal and focused. Preserve existing style and architecture unless instructed otherwise. If an attempt failed, clean up abandoned code before moving on. Bugfixes must fix the root cause when possible; do not patch only symptoms. Structure code cleanly from the start with sensible architecture, not everything in one file. Implement. Blocked? explain. Done? short summary.

Done: write one-line conventional commit msg to ".forge-ai-commit-msg". No commit. No push.`,
		ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number, branch, base, ticket.HTMLURL, ticket.Title, ticket.Body, strings.TrimSpace(ticket.Instruction), gitPolicy, toolSection)
}

func sessionIDFromInstruction(instruction string, routes []config.AgentRoute) string {
	lower := strings.ToLower(instruction)
	for _, route := range routes {
		mention := strings.ToLower(route.Mention)
		idx := strings.Index(lower, mention)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(instruction[idx+len(route.Mention):])
		if rest == "" {
			return ""
		}
		token := strings.Fields(rest)[0]
		if validSessionIDToken(token) {
			return token
		}
	}
	return ""
}

func validSessionIDToken(token string) bool {
	token = strings.Trim(token, "`'\"")
	if len(token) < 6 {
		return false
	}
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	if strings.ContainsAny(token, "-_:") {
		return true
	}
	return len(token) >= 16
}
