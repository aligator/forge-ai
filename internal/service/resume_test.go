package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

func TestManualResumeCreatesResumeRun(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	parentRun := runstore.Run{
		ID:           "parent-run-1",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 3,
		Branch:       "forge-ai/ac/demo/issue-3",
		BaseBranch:   "main",
		AgentMention: "@codex",
		AgentType:    "codex",
		SessionID:    "session-abc",
	}
	store.runs = append(store.runs, parentRun)

	agentResult := agent.Result{Output: "resumed", SessionID: "session-abc"}
	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Type: "codex", Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			CloneURLBase:         "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agentResult}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	runID, err := svc.ManualResume(context.Background(), "parent-run-1", "@codex", "", WorkspaceModeSameBranchFreshWorkspace, "continue the work", "alice")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}
	if runID == "" {
		t.Fatal("ManualResume() returned empty run ID")
	}

	// Wait for the async goroutine to complete
	deadline := time.After(5 * time.Second)
	for {
		run, _ := store.GetRun(context.Background(), runID)
		if run.Status == runstore.StatusSuccess || run.Status == runstore.StatusFailed || run.Status == runstore.StatusCanceled {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for resume run to complete")
		case <-time.After(10 * time.Millisecond):
		}
	}

	resumeRun, _ := store.GetRun(context.Background(), runID)
	if resumeRun.ID == "" {
		t.Fatalf("resume run %q not found in store", runID)
	}
	if resumeRun.Kind != runstore.RunKindManualResume {
		t.Fatalf("resume run kind = %q, want manual_resume", resumeRun.Kind)
	}
	if resumeRun.ParentRunID != "parent-run-1" {
		t.Fatalf("parent run ID = %q, want parent-run-1", resumeRun.ParentRunID)
	}
	if resumeRun.Status != runstore.StatusSuccess {
		t.Fatalf("resume run status = %q, want success", resumeRun.Status)
	}
	if resumeRun.Owner != "ac" || resumeRun.Repo != "demo" || resumeRun.TicketKind != "issue" || resumeRun.TicketNumber != 3 {
		t.Fatalf("resume run ticket fields = %+v", resumeRun)
	}
	if !store.hasEvent("queued") || !store.hasEvent("resume_start") || !store.hasEvent("workspace_ready") || !store.hasEvent("agent_finished") {
		t.Fatalf("events = %+v", store.events)
	}
}

func TestManualResumeInheritsSessionIDFromParent(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	store.runs = append(store.runs, runstore.Run{
		ID:           "parent-run-2",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 5,
		Branch:       "forge-ai/ac/demo/issue-5",
		BaseBranch:   "main",
		AgentMention: "@codex",
		SessionID:    "session-inherit",
	})

	capturedSessionID := ""
	ag := &captureSessionAgent{sessionID: func(s string) { capturedSessionID = s }}
	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			CloneURLBase:         "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": ag},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-2", "@codex", "", WorkspaceModeSameBranchFreshWorkspace, "continue", "bob")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		store.mu.Lock()
		n := len(store.statuses)
		store.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if capturedSessionID != "session-inherit" {
		t.Fatalf("session ID passed to agent = %q, want session-inherit", capturedSessionID)
	}
}

func TestManualResumeWorkspaceModeManualContextOnly(t *testing.T) {
	store := &recordingRunStore{}
	store.runs = append(store.runs, runstore.Run{
		ID:           "parent-run-3",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 7,
		Branch:       "main",
		BaseBranch:   "main",
		AgentMention: "@codex",
		SessionID:    "session-ctx",
	})

	gitPrepared := false
	git := &spyGit{prepare: func() { gitPrepared = true }, workdir: t.TempDir()}
	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      git,
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-3", "@codex", "", WorkspaceModeManualContextOnly, "context only", "carl")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		store.mu.Lock()
		n := len(store.statuses)
		store.mu.Unlock()
		if n > 0 {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out")
		case <-time.After(10 * time.Millisecond):
		}
	}

	if gitPrepared {
		t.Fatal("git.Prepare should not be called for manual_context_only mode")
	}
}

func TestManualResumeInvalidWorkspaceModeDefaultsToFreshWorkspace(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	store.runs = append(store.runs, runstore.Run{
		ID:           "parent-run-4",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 9,
		Branch:       "main",
		BaseBranch:   "main",
		AgentMention: "@codex",
		SessionID:    "session-x",
	})

	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			CloneURLBase:         "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-4", "@codex", "", "totally_invalid_mode", "do work", "dave")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}

	// Wait briefly for the run to start (workspace_ready confirms same_branch_fresh_workspace was used)
	deadline := time.After(5 * time.Second)
	for {
		if store.hasEvent("workspace_ready") {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for workspace_ready event")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestManualResumeRequiresNonEmptyPrompt(t *testing.T) {
	store := &recordingRunStore{}
	store.runs = append(store.runs, runstore.Run{ID: "parent-run-x", AgentMention: "@codex"})

	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Agents:   map[string]Agent{"@codex": &stubAgent{}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-x", "", "", "", "   ", "")
	if err == nil || err.Error() != "prompt is required" {
		t.Fatalf("expected 'prompt is required' error, got %v", err)
	}
}

func TestManualResumeCancellation(t *testing.T) {
	store := &recordingRunStore{}
	workdir := t.TempDir()
	store.runs = append(store.runs, runstore.Run{
		ID:           "parent-run-5",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 11,
		Branch:       "main",
		BaseBranch:   "main",
		AgentMention: "@codex",
		SessionID:    "session-cancel",
	})

	started := make(chan struct{})
	done := make(chan struct{})
	ag := &blockingAgent{started: started, done: done}

	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			CloneURLBase:         "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      &recordingGit{workdir: workdir},
		Agents:   map[string]Agent{"@codex": ag},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	runID, err := svc.ManualResume(context.Background(), "parent-run-5", "@codex", "", WorkspaceModeSameBranchFreshWorkspace, "do something", "eve")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}

	// Wait until agent has started
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not start in time")
	}

	canceled := svc.CancelRun(runID)
	if !canceled {
		t.Fatal("CancelRun() returned false")
	}

	// Unblock the agent so it can observe context cancellation
	close(done)

	deadline := time.After(5 * time.Second)
	for {
		run, _ := store.GetRun(context.Background(), runID)
		if run.Status == runstore.StatusCanceled {
			break
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for canceled status")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

// captureSessionAgent records the session ID it was invoked with.
type captureSessionAgent struct {
	sessionID func(string)
}

func (a *captureSessionAgent) Run(_ context.Context, _, _, sessionID string) (agent.Result, error) {
	a.sessionID(sessionID)
	return agent.Result{}, nil
}

func (a *captureSessionAgent) RunWithOptions(_ context.Context, opts agent.RunOptions) (agent.Result, error) {
	a.sessionID(opts.SessionID)
	return agent.Result{}, nil
}

// spyGit records whether Prepare was called.
type spyGit struct {
	workdir string
	prepare func()
}

func (g *spyGit) Prepare(_ context.Context, _, _, _, _, _, _, _ string, _ config.GitIdentity) (string, error) {
	if g.prepare != nil {
		g.prepare()
	}
	return g.workdir, nil
}

func (g *spyGit) CommitIfDirty(_ context.Context, _, _ string) (bool, error) { return false, nil }
func (g *spyGit) Push(_ context.Context, _, _ string) error                   { return nil }

// blockingAgent blocks until done is closed, then returns a context error.
type blockingAgent struct {
	started chan struct{}
	done    chan struct{}
}

func (a *blockingAgent) Run(ctx context.Context, _, _, _ string) (agent.Result, error) {
	return a.RunWithOptions(ctx, agent.RunOptions{})
}

func (a *blockingAgent) RunWithOptions(ctx context.Context, _ agent.RunOptions) (agent.Result, error) {
	close(a.started)
	<-a.done
	return agent.Result{}, errors.New("context canceled")
}
