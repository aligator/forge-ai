package config

import "testing"

func TestLoadAgentRoutesUsesPerRouteGitIdentity(t *testing.T) {
	t.Setenv("AGENT_0_USER", "codex")
	t.Setenv("AGENT_0_BIN", "codex")
	t.Setenv("AGENT_1_USER", "")
	t.Setenv("AGENT_0_GIT_USER_NAME", "Codex Bot")
	t.Setenv("AGENT_0_GIT_USER_EMAIL", "codex@example.invalid")

	routes := loadAgentRoutes(GitIdentity{
		UserName:  "forge-ai",
		UserEmail: "forge-ai@example.invalid",
	})

	if len(routes) != 1 {
		t.Fatalf("len(routes) = %d, want 1", len(routes))
	}
	if routes[0].Git.UserName != "Codex Bot" {
		t.Fatalf("Git.UserName = %q, want Codex Bot", routes[0].Git.UserName)
	}
	if routes[0].Git.UserEmail != "codex@example.invalid" {
		t.Fatalf("Git.UserEmail = %q, want codex@example.invalid", routes[0].Git.UserEmail)
	}
}

func TestLoadAgentRoutesFallsBackToDefaultGitIdentity(t *testing.T) {
	t.Setenv("AGENT_0_USER", "claude")
	t.Setenv("AGENT_0_BIN", "claude")
	t.Setenv("AGENT_1_USER", "")

	routes := loadAgentRoutes(GitIdentity{
		UserName:  "forge-ai",
		UserEmail: "forge-ai@example.invalid",
	})

	if got := routes[0].Git.UserName; got != "forge-ai" {
		t.Fatalf("Git.UserName = %q, want forge-ai", got)
	}
	if got := routes[0].Git.UserEmail; got != "forge-ai@example.invalid" {
		t.Fatalf("Git.UserEmail = %q, want forge-ai@example.invalid", got)
	}
}

func TestLoadAgentRoutesReturnsNoRoutesWithoutNumberedAgents(t *testing.T) {
	t.Setenv("AGENT_0_USER", "")

	routes := loadAgentRoutes(GitIdentity{
		UserName:  "forge-ai",
		UserEmail: "forge-ai@example.invalid",
	})

	if len(routes) != 0 {
		t.Fatalf("len(routes) = %d, want 0", len(routes))
	}
}
