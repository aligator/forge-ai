package service

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

func TestRetryRunCreatesQueuedChildRunAndAuditEvent(t *testing.T) {
	store := &recordingRunStore{}
	parentRun := runstore.Run{
		ID:           "parent-run-retry",
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusFailed,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 11,
		Branch:       "forge-ai/ac/demo/issue-11",
		BaseBranch:   "main",
		AgentMention: "@codex",
		AgentType:    "codex",
	}
	store.runs = append(store.runs, parentRun)

	svc := New(Options{
		Config: config.Config{
			Agents:               []config.AgentRoute{{User: "codex", Mention: "@codex", Agent: config.AgentConfig{Type: "codex", Timeout: 30 * time.Minute}}},
			WorkspaceDir:         t.TempDir(),
			ForgejoURL:           "http://forgejo.test",
			CloneURLBase:         "http://forgejo.test",
			MaxConcurrent:        1,
			ForgejoBootstrapUser: "forge-ai",
		},
		Forgejo:  &recordingForgejo{},
		Git:      &recordingGit{workdir: t.TempDir()},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	newRunID, err := svc.RetryRun(context.Background(), parentRun.ID, "alice")
	if err != nil {
		t.Fatalf("RetryRun() error = %v", err)
	}
	if newRunID == "" || newRunID == parentRun.ID {
		t.Fatalf("new run ID = %q", newRunID)
	}

	deadline := time.After(5 * time.Second)
	for {
		run, _ := store.GetRun(context.Background(), newRunID)
		if run.Status == runstore.StatusSuccess || run.Status == runstore.StatusFailed || run.Status == runstore.StatusCanceled {
			break
		}
		select {
		case <-deadline:
			t.Fatal("timed out waiting for retry run")
		case <-time.After(10 * time.Millisecond):
		}
	}

	retryRun, _ := store.GetRun(context.Background(), newRunID)
	if retryRun.ParentRunID != parentRun.ID {
		t.Fatalf("retry parent = %q, want %q", retryRun.ParentRunID, parentRun.ID)
	}
	if retryRun.Owner != parentRun.Owner || retryRun.Repo != parentRun.Repo || retryRun.TicketNumber != parentRun.TicketNumber {
		t.Fatalf("retry context = %+v, want parent context %+v", retryRun, parentRun)
	}
	if retryRun.Status != runstore.StatusSuccess {
		t.Fatalf("retry status = %q, want success", retryRun.Status)
	}
	if len(store.audit) != 1 {
		t.Fatalf("audit events = %+v, want 1", store.audit)
	}
	if store.audit[0].Actor != "alice" || store.audit[0].Action != "run.retry" || store.audit[0].TargetID != parentRun.ID || !strings.Contains(store.audit[0].DataJSON, newRunID) {
		t.Fatalf("audit event = %+v", store.audit[0])
	}
}

func TestRetryRunRejectsNonFailedOrCanceledRun(t *testing.T) {
	store := &recordingRunStore{}
	store.runs = append(store.runs, runstore.Run{
		ID:           "run-active",
		Status:       runstore.StatusRunning,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 12,
		Branch:       "work",
		BaseBranch:   "main",
		AgentMention: "@codex",
	})
	svc := New(Options{
		Config: config.Config{
			Agents:        []config.AgentRoute{{Mention: "@codex"}},
			MaxConcurrent: 1,
		},
		Agents:   map[string]Agent{"@codex": &streamingStubAgent{result: agent.Result{}}},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	_, err := svc.RetryRun(context.Background(), "run-active", "alice")
	if err == nil || !strings.Contains(err.Error(), "cannot be retried") {
		t.Fatalf("RetryRun() error = %v, want cannot be retried", err)
	}
	if len(store.audit) != 0 {
		t.Fatalf("audit events = %+v, want none", store.audit)
	}
}

func TestCancelRunAsWritesAuditEventOnSuccess(t *testing.T) {
	store := &recordingRunStore{}
	svc := New(Options{
		Config:   config.Config{MaxConcurrent: 1},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	runID := "run-cancel"
	canceled := false
	svc.registerCancel(runID, func() {
		canceled = true
	})

	if !svc.CancelRunAs(context.Background(), runID, "alice") {
		t.Fatal("CancelRunAs() = false, want true")
	}
	if !canceled {
		t.Fatal("registered cancel func was not called")
	}
	if len(store.audit) != 1 {
		t.Fatalf("audit events = %+v, want 1", store.audit)
	}
	if store.audit[0].Actor != "alice" || store.audit[0].Action != "run.cancel" || store.audit[0].TargetType != "run" || store.audit[0].TargetID != runID {
		t.Fatalf("audit event = %+v", store.audit[0])
	}
}

func TestCancelRunAsCancelsStaleStoredRun(t *testing.T) {
	store := &recordingRunStore{}
	runID := "run-stale"
	store.runs = append(store.runs, runstore.Run{
		ID:     runID,
		Status: runstore.StatusRunning,
	})
	svc := New(Options{
		Config:   config.Config{MaxConcurrent: 1},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	if !svc.CancelRunAs(context.Background(), runID, "alice") {
		t.Fatal("CancelRunAs() = false, want true")
	}

	run, err := store.GetRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if run.Status != runstore.StatusCanceled {
		t.Fatalf("status = %q, want canceled", run.Status)
	}
	if run.FinishedAt.IsZero() {
		t.Fatal("finished_at was not set")
	}
	if !strings.Contains(run.Error, "stale") {
		t.Fatalf("error = %q, want stale message", run.Error)
	}
	if !store.hasEvent("canceled") {
		t.Fatal("canceled event was not recorded")
	}
	if len(store.audit) != 1 {
		t.Fatalf("audit events = %+v, want 1", store.audit)
	}
	if store.audit[0].Actor != "alice" || store.audit[0].Action != "run.cancel" || !strings.Contains(store.audit[0].DataJSON, "stale") {
		t.Fatalf("audit event = %+v", store.audit[0])
	}
}

func TestQueuePauseResumeWritesAuditEvents(t *testing.T) {
	store := &recordingRunStore{}
	svc := New(Options{
		Config:   config.Config{MaxConcurrent: 1},
		RunStore: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	})

	svc.PauseQueue(context.Background(), "alice")
	if !svc.RuntimeSnapshot().Paused {
		t.Fatal("queue is not paused")
	}
	svc.ResumeQueue(context.Background(), "bob")
	if svc.RuntimeSnapshot().Paused {
		t.Fatal("queue is still paused")
	}
	if len(store.audit) != 2 {
		t.Fatalf("audit events = %+v, want 2", store.audit)
	}
	if store.audit[0].Action != "queue.pause" || store.audit[0].Actor != "alice" {
		t.Fatalf("pause audit = %+v", store.audit[0])
	}
	if store.audit[1].Action != "queue.resume" || store.audit[1].Actor != "bob" {
		t.Fatalf("resume audit = %+v", store.audit[1])
	}
}
