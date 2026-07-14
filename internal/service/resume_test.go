package service

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
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

func TestManualResumeWorkspaceModeExistingWorkspace(t *testing.T) {
	store := &recordingRunStore{}
	workspaceRoot := t.TempDir()
	parentRun := runstore.Run{
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
	}
	existingWorkdir := resumeWorkspacePath(workspaceRoot, parentRun)
	if err := os.MkdirAll(filepath.Join(existingWorkdir, ".git"), 0o755); err != nil {
		t.Fatalf("create existing workspace: %v", err)
	}
	store.runs = append(store.runs, parentRun)

	gitPrepared := false
	git := &spyGit{prepare: func() { gitPrepared = true }, workdir: t.TempDir()}
	agentWorkdir := ""
	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         workspaceRoot,
			ForgejoURL:           "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      git,
		Agents:   map[string]Agent{"@codex": &captureWorkdirAgent{workdir: func(w string) { agentWorkdir = w }}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-4", "@codex", "", WorkspaceModeExistingWorkspace, "use existing", "dave")
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

	if !gitPrepared {
		t.Fatal("git.Prepare should be called for existing_workspace mode")
	}
	if agentWorkdir != git.workdir {
		t.Fatalf("agent workdir = %q, want %q", agentWorkdir, git.workdir)
	}
}

func TestManualResumeWorkspaceModeExistingWorkspacePreparesWorkspace(t *testing.T) {
	store := &recordingRunStore{}
	parentRun := runstore.Run{
		ID:           "parent-run-missing-workspace",
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
	}
	store.runs = append(store.runs, parentRun)

	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Git:      &spyGit{workdir: t.TempDir()},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	runID, err := svc.ManualResume(context.Background(), parentRun.ID, "@codex", "", WorkspaceModeExistingWorkspace, "use existing", "dave")
	if err != nil {
		t.Fatalf("ManualResume() error = %v", err)
	}

	deadline := time.After(5 * time.Second)
	for {
		run, _ := store.GetRun(context.Background(), runID)
		if run.Status == runstore.StatusSuccess {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for success status")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestManualResumeInvalidWorkspaceModeReturnsError(t *testing.T) {
	store := &recordingRunStore{}
	store.runs = append(store.runs, runstore.Run{
		ID:           "parent-run-invalid-mode",
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
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.ManualResume(context.Background(), "parent-run-invalid-mode", "@codex", "", "totally_invalid_mode", "do work", "dave")
	if err == nil || err.Error() != `invalid workspace mode "totally_invalid_mode"` {
		t.Fatalf("ManualResume() error = %v, want invalid workspace mode", err)
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

func TestCreateResumeRunRequiresRunStore(t *testing.T) {
	runner := &workflowRunner{}

	_, err := runner.createResumeRun(context.Background(), runstore.Run{}, manualResumeInput{})
	if err == nil || err.Error() != "run store not configured" {
		t.Fatalf("createResumeRun() error = %v, want run store not configured", err)
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

type captureWorkdirAgent struct {
	workdir func(string)
}

func (a *captureWorkdirAgent) Run(_ context.Context, workdir, _, _ string) (agent.Result, error) {
	a.workdir(workdir)
	return agent.Result{}, nil
}

func (a *captureWorkdirAgent) RunWithOptions(_ context.Context, opts agent.RunOptions) (agent.Result, error) {
	a.workdir(opts.Workdir)
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
func (g *spyGit) Push(_ context.Context, _, _ string) error                  { return nil }

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
	return agent.Result{}, ctx.Err()
}
