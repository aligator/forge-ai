package service

import (
	"strings"
	"testing"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
)

func TestPromptTellsAgentToCommunicateViaForgejoMCP(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai hello",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"read issue/PR comments via Forgejo MCP",
		"Post a Forgejo comment via Forgejo MCP",
		"short progress updates",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptTellsAgentWhenNotToImplement(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai what would you change?",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"If the user asks clear questions or asks for analysis/advice, answer only",
		"do not implement unless the user asks for code changes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptTellsAgentToKeepImplementationClean(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai implement",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"Keep changes minimal and focused",
		"Preserve existing style and architecture",
		"clean up abandoned code",
		"fix the root cause when possible",
		"Structure code cleanly from the start",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptTellsAgentToMergeBaseBeforeFinishing(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai implement",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"Merge assistance:",
		"fetch the base branch and merge it into the current branch",
		"If the merge has conflicts you can confidently resolve",
		"abort the merge, keep your task changes intact",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestSessionIDFromInstructionUsesTokenAfterMention(t *testing.T) {
	routes := []config.AgentRoute{{Mention: "@claude"}}
	got := sessionIDFromInstruction("@claude session-123 continue work", routes)
	if got != "session-123" {
		t.Fatalf("sessionIDFromInstruction() = %q, want session-123", got)
	}
}

func TestSessionIDFromInstructionIgnoresNormalTaskText(t *testing.T) {
	routes := []config.AgentRoute{{Mention: "@claude"}}
	got := sessionIDFromInstruction("@claude fix bug", routes)
	if got != "" {
		t.Fatalf("sessionIDFromInstruction() = %q, want empty", got)
	}
}
