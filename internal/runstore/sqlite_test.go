package runstore

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestSQLiteStorePersistsRunsEventsLogsAndLinks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runstore.sqlite")

	store, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}

	started := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	run, err := store.CreateRun(ctx, CreateRunInput{
		Kind:         RunKindWebhookRun,
		Status:       StatusQueued,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 3,
		Branch:       "forge-ai/ac/demo/issue-3",
		BaseBranch:   "main",
		AgentMention: "@codex",
		AgentType:    "codex",
		StartedAt:    started,
		CreatedBy:    "alice",
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, run.ID, StatusRunning, time.Time{}, ""); err != nil {
		t.Fatalf("UpdateRunStatus(running) error = %v", err)
	}
	if err := store.SetSessionID(ctx, run.ID, "session-123"); err != nil {
		t.Fatalf("SetSessionID() error = %v", err)
	}
	if err := store.AddEvent(ctx, EventInput{RunID: run.ID, Type: "workspace_ready", Message: "prepared", DataJSON: `{"ok":true}`}); err != nil {
		t.Fatalf("AddEvent() error = %v", err)
	}
	if err := store.AddLogChunk(ctx, LogChunkInput{RunID: run.ID, Stream: "combined", Chunk: "line 1\nline 2\n"}); err != nil {
		t.Fatalf("AddLogChunk() error = %v", err)
	}
	if err := store.AddLink(ctx, LinkInput{RunID: run.ID, Type: "pull_request", URL: "https://forge/pr/1", Label: "PR #1"}); err != nil {
		t.Fatalf("AddLink() error = %v", err)
	}
	finished := started.Add(2 * time.Minute)
	if err := store.UpdateRunStatus(ctx, run.ID, StatusSuccess, finished, ""); err != nil {
		t.Fatalf("UpdateRunStatus(success) error = %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("reopen OpenSQLite() error = %v", err)
	}
	defer reopened.Close()

	got, err := reopened.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got.Status != StatusSuccess || got.SessionID != "session-123" || got.FinishedAt.IsZero() {
		t.Fatalf("persisted run = %+v, want success with session and finish time", got)
	}

	events, err := reopened.ListEvents(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListEvents() error = %v", err)
	}
	if len(events) != 1 || events[0].DataJSON != `{"ok":true}` {
		t.Fatalf("events = %+v", events)
	}
	logs, err := reopened.ListLogChunks(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListLogChunks() error = %v", err)
	}
	if len(logs) != 1 || logs[0].Chunk != "line 1\nline 2\n" {
		t.Fatalf("logs = %+v", logs)
	}
	links, err := reopened.ListLinks(ctx, run.ID)
	if err != nil {
		t.Fatalf("ListLinks() error = %v", err)
	}
	if len(links) != 1 || links[0].Type != "pull_request" || links[0].URL == "" {
		t.Fatalf("links = %+v", links)
	}
}

func TestSQLiteStorePersistsSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runstore.sqlite")

	store, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}

	if _, err := store.GetSetting(ctx, "max_concurrent"); !errors.Is(err, ErrSettingNotFound) {
		t.Fatalf("GetSetting() error = %v, want ErrSettingNotFound", err)
	}

	if err := store.SetSetting(ctx, "max_concurrent", "3", "alice"); err != nil {
		t.Fatalf("SetSetting() error = %v", err)
	}
	if err := store.SetSetting(ctx, "max_concurrent", "5", "bob"); err != nil {
		t.Fatalf("SetSetting() overwrite error = %v", err)
	}
	store.Close()

	reopened, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("reopen OpenSQLite() error = %v", err)
	}
	defer reopened.Close()

	value, err := reopened.GetSetting(ctx, "max_concurrent")
	if err != nil {
		t.Fatalf("GetSetting() after reopen error = %v", err)
	}
	if value != "5" {
		t.Fatalf("GetSetting() = %q, want 5", value)
	}
}

func TestSQLiteStorePersistsAuditEvents(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "runstore.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer store.Close()

	ts := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	if err := store.AddAuditEvent(ctx, AuditEventInput{
		Time:       ts,
		Actor:      "alice",
		Action:     "run.cancel",
		TargetType: "run",
		TargetID:   "run-1",
		DataJSON:   `{"reason":"operator"}`,
	}); err != nil {
		t.Fatalf("AddAuditEvent() error = %v", err)
	}

	events, err := store.ListAuditEvents(ctx, ListAuditEventsOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents() error = %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit events = %d, want 1", len(events))
	}
	got := events[0]
	if got.Actor != "alice" || got.Action != "run.cancel" || got.TargetID != "run-1" || got.DataJSON != `{"reason":"operator"}` {
		t.Fatalf("audit event = %+v", got)
	}
}

func TestSQLiteStorePersistsAgentSettings(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "runstore.sqlite")
	store, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	saved, err := store.UpsertAgentSettings(ctx, UpsertAgentSettingsInput{
		Mention:     "@codex",
		Enabled:     true,
		Model:       "gpt-test",
		Args:        []string{"--model", "gpt-test"},
		Timeout:     42 * time.Minute,
		ToolHints:   "- use rtk",
		AllowGit:    true,
		AllowGitSet: true,
		UpdatedBy:   "alice",
	})
	if err != nil {
		t.Fatalf("UpsertAgentSettings() error = %v", err)
	}
	if saved.Mention != "@codex" || !saved.AllowGitSet {
		t.Fatalf("saved settings = %+v", saved)
	}
	if err := store.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := OpenSQLite(ctx, path)
	if err != nil {
		t.Fatalf("reopen OpenSQLite() error = %v", err)
	}
	defer reopened.Close()
	got, err := reopened.GetAgentSettings(ctx, "@codex")
	if err != nil {
		t.Fatalf("GetAgentSettings() error = %v", err)
	}
	if !got.Enabled || got.Model != "gpt-test" || got.Timeout != 42*time.Minute || got.ToolHints != "- use rtk" || !got.AllowGit || !got.AllowGitSet || got.UpdatedBy != "alice" {
		t.Fatalf("settings = %+v", got)
	}
	if len(got.Args) != 2 || got.Args[0] != "--model" || got.Args[1] != "gpt-test" {
		t.Fatalf("args = %#v", got.Args)
	}
	all, err := reopened.ListAgentSettings(ctx)
	if err != nil {
		t.Fatalf("ListAgentSettings() error = %v", err)
	}
	if len(all) != 1 || all[0].Mention != "@codex" {
		t.Fatalf("all settings = %+v", all)
	}
	if err := reopened.DeleteAgentSettings(ctx, "@codex"); err != nil {
		t.Fatalf("DeleteAgentSettings() error = %v", err)
	}
	if _, err := reopened.GetAgentSettings(ctx, "@codex"); !errors.Is(err, ErrAgentSettingsNotFound) {
		t.Fatalf("GetAgentSettings() after delete error = %v, want %v", err, ErrAgentSettingsNotFound)
	}
}

func TestSQLiteStoreRejectsInvalidStatusTransition(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "runstore.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer store.Close()

	run, err := store.CreateRun(ctx, CreateRunInput{
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 3,
		Branch:       "work",
		BaseBranch:   "main",
		AgentMention: "@codex",
	})
	if err != nil {
		t.Fatalf("CreateRun() error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, run.ID, StatusSuccess, time.Now(), ""); !errors.Is(err, ErrInvalidStatusTransition) {
		t.Fatalf("UpdateRunStatus(queued->success) error = %v, want ErrInvalidStatusTransition", err)
	}
	got, err := store.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatalf("GetRun() error = %v", err)
	}
	if got.Status != StatusQueued {
		t.Fatalf("status = %s, want queued", got.Status)
	}
}

func TestSQLiteStoreAllowsQueuedAndRunningCancelTransitions(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "runstore.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer store.Close()

	queued, err := store.CreateRun(ctx, CreateRunInput{
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 3,
		Branch:       "queued",
		BaseBranch:   "main",
		AgentMention: "@codex",
	})
	if err != nil {
		t.Fatalf("CreateRun(queued) error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, queued.ID, StatusCanceled, time.Now(), "operator canceled"); err != nil {
		t.Fatalf("UpdateRunStatus(queued->canceled) error = %v", err)
	}

	running, err := store.CreateRun(ctx, CreateRunInput{
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 4,
		Branch:       "running",
		BaseBranch:   "main",
		AgentMention: "@codex",
	})
	if err != nil {
		t.Fatalf("CreateRun(running) error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, running.ID, StatusRunning, time.Time{}, ""); err != nil {
		t.Fatalf("UpdateRunStatus(queued->running) error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, running.ID, StatusCanceled, time.Now(), "operator canceled"); err != nil {
		t.Fatalf("UpdateRunStatus(running->canceled) error = %v", err)
	}
}

func TestSQLiteStoreListRunsFiltersAndSorts(t *testing.T) {
	ctx := context.Background()
	store, err := OpenSQLite(ctx, filepath.Join(t.TempDir(), "runstore.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer store.Close()

	start := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	first, err := store.CreateRun(ctx, CreateRunInput{
		Status:       StatusQueued,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 1,
		Branch:       "one",
		BaseBranch:   "main",
		AgentMention: "@claude",
		StartedAt:    start,
	})
	if err != nil {
		t.Fatalf("CreateRun(first) error = %v", err)
	}
	second, err := store.CreateRun(ctx, CreateRunInput{
		Status:       StatusQueued,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 2,
		Branch:       "two",
		BaseBranch:   "main",
		AgentMention: "@codex",
		StartedAt:    start.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("CreateRun(second) error = %v", err)
	}
	if err := store.UpdateRunStatus(ctx, first.ID, StatusRunning, time.Time{}, ""); err != nil {
		t.Fatalf("UpdateRunStatus(first) error = %v", err)
	}

	runs, err := store.ListRuns(ctx, ListRunsOptions{Status: StatusQueued, Sort: "agent", Desc: false})
	if err != nil {
		t.Fatalf("ListRuns() error = %v", err)
	}
	if len(runs) != 1 || runs[0].ID != second.ID {
		t.Fatalf("queued runs = %+v, want only %s", runs, second.ID)
	}

	runs, err = store.ListRuns(ctx, ListRunsOptions{Sort: "agent", Desc: false})
	if err != nil {
		t.Fatalf("ListRuns(agent) error = %v", err)
	}
	if len(runs) != 2 || runs[0].AgentMention != "@claude" || runs[1].AgentMention != "@codex" {
		t.Fatalf("agent-sorted runs = %+v", runs)
	}
}
