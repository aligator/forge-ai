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

func TestPromptAllowsGitWhenAWorkflowNeedsIt(t *testing.T) {
	ticket := forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai implement",
	}

	off := prompt(ticket, "forge-ai/ac/demo/issue-1", "main", false, "")
	for _, want := range []string{
		"Default flow: edit files only",
		"Git is not forbidden — run git commands when a workflow needs them",
		"Never create/switch/reset/rebase/delete branches or rewrite existing history.",
		"Leave committing and pushing to forge-ai unless a workflow forced you to do it yourself.",
	} {
		if !strings.Contains(off, want) {
			t.Fatalf("prompt(allowGit=false) missing %q in:\n%s", want, off)
		}
	}
	for _, unwanted := range []string{"No git cmds", "No commit. No push."} {
		if strings.Contains(off, unwanted) {
			t.Fatalf("prompt(allowGit=false) still contains %q in:\n%s", unwanted, off)
		}
	}

	on := prompt(ticket, "forge-ai/ac/demo/issue-1", "main", true, "")
	for _, want := range []string{
		"Push only when a workflow requires it",
		"Never create/switch/reset/rebase/delete branches or rewrite existing history.",
	} {
		if !strings.Contains(on, want) {
			t.Fatalf("prompt(allowGit=true) missing %q in:\n%s", want, on)
		}
	}
	if strings.Contains(on, "Forbidden: create/switch/reset/rebase/delete branches, push.") {
		t.Fatalf("prompt(allowGit=true) still forbids push outright in:\n%s", on)
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
