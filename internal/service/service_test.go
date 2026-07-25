package service

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
)

func TestShouldRunOnlyForMention(t *testing.T) {
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "forge-ai", Mention: "@forge-ai"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
	})

	if !svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-user"}}, forgejo.Ticket{Instruction: "@forge-ai say hello"}) {
		t.Fatal("expected mention to trigger")
	}

	if svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-user"}}, forgejo.Ticket{Labels: []forgejo.Label{{Name: "ai"}}}) {
		t.Fatal("expected label alone not to trigger")
	}

	if svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-ai"}}, forgejo.Ticket{Instruction: "@forge-ai done"}) {
		t.Fatal("expected bot mention not to trigger")
	}
}

func TestShouldRunForAnyConfiguredMention(t *testing.T) {
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex"},
				{User: "claude", Mention: "@claude"},
				{User: "opencode", Mention: "@opencode"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
	})

	for _, mention := range []string{"@codex", "@claude", "@opencode"} {
		if !svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "user"}}, forgejo.Ticket{Instruction: mention + " do something"}) {
			t.Fatalf("expected %q to trigger", mention)
		}
	}

	if svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "user"}}, forgejo.Ticket{Instruction: "@other do something"}) {
		t.Fatal("expected unknown mention not to trigger")
	}
}

func TestShouldRunUsesStoredAgentEnabledSetting(t *testing.T) {
	store := &recordingRunStore{
		settings: map[string]runstore.AgentSettings{
			"@codex": {
				Mention: "@codex",
				Enabled: false,
			},
		},
	}
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
		RunStore: store,
	})

	if svc.handler.shouldRun(context.Background(), forgejo.WebhookPayload{Sender: &forgejo.User{Login: "user"}}, forgejo.Ticket{Instruction: "@codex do something"}) {
		t.Fatal("expected stored disabled setting to prevent trigger")
	}
}

func TestFindAgentPicksCorrectRunner(t *testing.T) {
	codexRunner := &stubAgent{name: "codex"}
	claudeRunner := &stubAgent{name: "claude"}

	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex"},
				{User: "claude", Mention: "@claude"},
			},
			MaxConcurrent: 1,
		},
		Agents: map[string]Agent{
			"@codex":  codexRunner,
			"@claude": claudeRunner,
		},
	})

	if _, got := svc.handler.findAgent(context.Background(), "@codex please fix this"); got != codexRunner {
		t.Fatal("expected codex runner")
	}
	if _, got := svc.handler.findAgent(context.Background(), "@claude please fix this"); got != claudeRunner {
		t.Fatal("expected claude runner")
	}
}

func TestHandleReviewedWebhookFetchesTriggeredReviewComments(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	forge := &recordingForgejo{
		reviewComments: []string{"@codex handle the final review feedback"},
	}
	agent := &capturePromptAgent{}
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Type: "codex"}},
			},
			WorkspaceDir:         t.TempDir(),
			BranchPrefix:         "forge-ai",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Forgejo:  forge,
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": agent},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	err := svc.Handle(context.Background(), "pull_request_comment", forgejo.WebhookPayload{
		Action: "reviewed",
		Repository: forgejo.Repository{
			Name:          "demo",
			CloneURL:      "https://forgejo.example/ac/demo.git",
			DefaultBranch: "main",
			Owner:         forgejo.User{Login: "ac"},
		},
		Pull: &forgejo.PullRequest{
			Index:   8,
			Title:   "Fix review flow",
			HTMLURL: "https://forgejo.example/ac/demo/pulls/8",
			Head: forgejo.PullRequestBranch{
				Ref:  "review-flow",
				Repo: forgejo.Repository{CloneURL: "https://forgejo.example/ac/demo.git"},
			},
			Base: forgejo.PullRequestBranch{Ref: "main"},
		},
		Review: &forgejo.Review{ID: 123},
		Sender: &forgejo.User{Login: "alice"},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if forge.reviewID != 123 {
		t.Fatalf("reviewID = %d, want 123", forge.reviewID)
	}
	if !strings.Contains(agent.prompt, "Trigger comment:\n@codex handle the final review feedback") {
		t.Fatalf("prompt missing triggered review comment:\n%s", agent.prompt)
	}
	if len(store.runs) != 1 || store.runs[0].Status != runstore.StatusSuccess {
		t.Fatalf("runs = %+v, want one successful run", store.runs)
	}
}

func TestHandleIgnoresDeletedComment(t *testing.T) {
	triggered := false
	spy := &spyAgent{onRun: func() { triggered = true }}
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "forge-ai", Mention: "@forge-ai"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
		Agents: map[string]Agent{
			"@forge-ai": spy,
		},
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	err := svc.Handle(context.Background(), "issue_comment", forgejo.WebhookPayload{
		Action: "deleted",
		Repository: forgejo.Repository{
			Name:          "demo",
			DefaultBranch: "main",
			Owner:         forgejo.User{Login: "ac"},
		},
		Issue:   &forgejo.Issue{Index: 1, Title: "test"},
		Comment: &forgejo.Comment{ID: 42, Body: "@forge-ai do something"},
		Sender:  &forgejo.User{Login: "alice"},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}
	if triggered {
		t.Fatal("agent must not be triggered on comment deletion")
	}
}

func TestHandleRecordsRunStatusEventsLogsAndLinks(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	forge := &recordingForgejo{
		openPullRequest: &forgejo.PullRequest{Index: 9, HTMLURL: "https://forgejo.example/ac/demo/pulls/9"},
	}
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Type: "codex"}},
			},
			WorkspaceDir:          t.TempDir(),
			BranchPrefix:          "forge-ai",
			CreatePR:              true,
			MaxConcurrent:         1,
			ForgejoBootstrapUser:  "forge-ai",
			ForgejoToken:          "token",
			ForgejoBootstrapToken: "forge-ai-local",
		},
		Forgejo:  forge,
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": &stubAgent{result: agent.Result{Output: "one chunk\nwith multiple lines", SessionID: "session-123"}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	err := svc.Handle(context.Background(), "issue_comment", forgejo.WebhookPayload{
		Action: "created",
		Repository: forgejo.Repository{
			Name:          "demo",
			CloneURL:      "https://forgejo.example/ac/demo.git",
			DefaultBranch: "main",
			Owner:         forgejo.User{Login: "ac"},
		},
		Issue: &forgejo.Issue{
			Index:   3,
			Title:   "Add store",
			HTMLURL: "https://forgejo.example/ac/demo/issues/3",
		},
		Comment: &forgejo.Comment{ID: 42, Body: "@codex implement"},
		Sender:  &forgejo.User{Login: "alice"},
	})
	if err != nil {
		t.Fatalf("Handle() error = %v", err)
	}

	if len(store.runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(store.runs))
	}
	run := store.runs[0]
	if run.Kind != runstore.RunKindWebhookRun || run.Status != runstore.StatusSuccess {
		t.Fatalf("run status = %s/%s, want webhook_run/success", run.Kind, run.Status)
	}
	if run.Owner != "ac" || run.Repo != "demo" || run.TicketKind != "issue" || run.TicketNumber != 3 {
		t.Fatalf("run ticket fields = %+v", run)
	}
	if run.Branch != "forge-ai/ac/demo/issue-3" || run.BaseBranch != "main" || run.AgentMention != "@codex" || run.AgentType != "codex" {
		t.Fatalf("run workflow fields = %+v", run)
	}
	if run.SessionID != "session-123" {
		t.Fatalf("session id = %q, want session-123", run.SessionID)
	}
	if store.statuses[0] != runstore.StatusRunning || store.statuses[len(store.statuses)-1] != runstore.StatusSuccess {
		t.Fatalf("statuses = %+v, want running then success", store.statuses)
	}
	if len(store.logs) != 1 || store.logs[0].Chunk != "one chunk\nwith multiple lines" {
		t.Fatalf("logs = %+v", store.logs)
	}
	if !store.hasEvent("queued") || !store.hasEvent("workspace_ready") || !store.hasEvent("agent_finished") || !store.hasEvent("success") {
		t.Fatalf("events = %+v", store.events)
	}
	if !store.hasLink("ticket", "https://forgejo.example/ac/demo/issues/3") || !store.hasLink("pull_request", "https://forgejo.example/ac/demo/pulls/9") {
		t.Fatalf("links = %+v", store.links)
	}
}
