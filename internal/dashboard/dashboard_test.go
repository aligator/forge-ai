package dashboard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

type memoryStore struct {
	run      runstore.Run
	runs     []runstore.Run
	events   []runstore.Event
	logs     []runstore.LogChunk
	links    []runstore.Link
	audit    []runstore.AuditEvent
	settings map[string]runstore.AgentSettings
	lastOpt  runstore.ListRunsOptions
}

type failingResumer struct {
	cancelOK bool
}

type noopActions struct{}

type recordingActions struct {
	cancelRunID string
	retryRunID  string
	retryNewID  string
	paused      bool
	resumed     bool
	actor       string
}

func (r failingResumer) ManualResume(context.Context, string, string, string, string, string, string) (string, error) {
	return "", errors.New("resume failed")
}

func (r failingResumer) CancelRun(string) bool {
	return r.cancelOK
}

func (noopActions) CancelRunAs(context.Context, string, string) bool {
	return true
}

func (noopActions) RetryRun(context.Context, string, string) (string, error) {
	return "retry-run", nil
}

func (noopActions) PauseQueue(context.Context, string) {}

func (noopActions) ResumeQueue(context.Context, string) {}

func (a *recordingActions) CancelRunAs(_ context.Context, id, actor string) bool {
	a.cancelRunID = id
	a.actor = actor
	return true
}

func (a *recordingActions) RetryRun(_ context.Context, parentRunID, actor string) (string, error) {
	a.retryRunID = parentRunID
	a.actor = actor
	if a.retryNewID != "" {
		return a.retryNewID, nil
	}
	return "retry-run", nil
}

func (a *recordingActions) PauseQueue(_ context.Context, actor string) {
	a.paused = true
	a.actor = actor
}

func (a *recordingActions) ResumeQueue(_ context.Context, actor string) {
	a.resumed = true
	a.actor = actor
}

func (s *memoryStore) ListRuns(_ context.Context, opts runstore.ListRunsOptions) ([]runstore.Run, error) {
	s.lastOpt = opts
	if len(s.runs) > 0 {
		return s.runs, nil
	}
	return []runstore.Run{s.run}, nil
}

func (s *memoryStore) GetRun(context.Context, string) (runstore.Run, error) {
	return s.run, nil
}

func (s *memoryStore) ListEvents(context.Context, string) ([]runstore.Event, error) {
	return s.events, nil
}

func (s *memoryStore) ListEventsSince(_ context.Context, _ string, sinceID int64) ([]runstore.Event, error) {
	return filterSince(s.events, sinceID, func(e runstore.Event) int64 { return e.ID }), nil
}

func (s *memoryStore) ListLogChunks(context.Context, string) ([]runstore.LogChunk, error) {
	return s.logs, nil
}

func (s *memoryStore) ListLogChunksSince(_ context.Context, _ string, sinceID int64) ([]runstore.LogChunk, error) {
	return filterSince(s.logs, sinceID, func(c runstore.LogChunk) int64 { return c.ID }), nil
}

func filterSince[T any](items []T, sinceID int64, id func(T) int64) []T {
	var out []T
	for _, item := range items {
		if id(item) > sinceID {
			out = append(out, item)
		}
	}
	return out
}

func (s *memoryStore) ListLinks(context.Context, string) ([]runstore.Link, error) {
	return s.links, nil
}

func (s *memoryStore) ListAuditEvents(context.Context, runstore.ListAuditEventsOptions) ([]runstore.AuditEvent, error) {
	return s.audit, nil
}

func (s *memoryStore) AddAuditEvent(_ context.Context, in runstore.AuditEventInput) error {
	s.audit = append(s.audit, runstore.AuditEvent{
		Actor:      in.Actor,
		Action:     in.Action,
		TargetType: in.TargetType,
		TargetID:   in.TargetID,
		DataJSON:   in.DataJSON,
	})
	return nil
}

func (s *memoryStore) GetAgentSettings(_ context.Context, mention string) (runstore.AgentSettings, error) {
	if s.settings == nil {
		return runstore.AgentSettings{}, runstore.ErrAgentSettingsNotFound
	}
	settings, ok := s.settings[mention]
	if !ok {
		return runstore.AgentSettings{}, runstore.ErrAgentSettingsNotFound
	}
	return settings, nil
}

func (s *memoryStore) UpsertAgentSettings(_ context.Context, in runstore.UpsertAgentSettingsInput) (runstore.AgentSettings, error) {
	if s.settings == nil {
		s.settings = make(map[string]runstore.AgentSettings)
	}
	settings := runstore.AgentSettings{
		Mention:     in.Mention,
		Enabled:     in.Enabled,
		Model:       in.Model,
		Args:        append([]string(nil), in.Args...),
		Timeout:     in.Timeout,
		ToolHints:   in.ToolHints,
		AllowGit:    in.AllowGit,
		AllowGitSet: in.AllowGitSet,
		UpdatedAt:   time.Now().UTC(),
		UpdatedBy:   in.UpdatedBy,
	}
	s.settings[in.Mention] = settings
	return settings, nil
}

func (s *memoryStore) DeleteAgentSettings(_ context.Context, mention string) error {
	if s.settings != nil {
		delete(s.settings, mention)
	}
	return nil
}

func TestRunEventsStreamsStoredEventsAndLogs(t *testing.T) {
	store := memoryStore{
		run: runstore.Run{ID: "run-1", Status: runstore.StatusRunning},
		events: []runstore.Event{{
			ID:      1,
			RunID:   "run-1",
			Time:    time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
			Type:    "running",
			Message: "workflow started",
		}},
		logs: []runstore.LogChunk{{
			ID:     1,
			RunID:  "run-1",
			Time:   time.Date(2026, 7, 5, 12, 0, 1, 0, time.UTC),
			Stream: "stdout",
			Chunk:  "hello\n",
		}},
	}
	handler := New(config.Config{ForgejoURL: "https://forgejo.example.test", MaxConcurrent: 1}, &store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/dashboard/runs/run-1/events", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		defer close(done)
		mux.ServeHTTP(rec, req)
	}()

	deadline := time.After(2 * time.Second)
	for {
		if strings.Contains(rec.Body.String(), "event: run_event") && strings.Contains(rec.Body.String(), "event: run_log") {
			cancel()
			break
		}
		select {
		case <-deadline:
			cancel()
			t.Fatalf("SSE body missing events/logs:\n%s", rec.Body.String())
		default:
			time.Sleep(10 * time.Millisecond)
		}
	}
	<-done

	if rec.Code != http.StatusOK {
		t.Fatalf("SSE status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}
}

func TestRunsPagePassesFilterAndSortOptions(t *testing.T) {
	store := &memoryStore{run: runstore.Run{ID: "run-1", Status: runstore.StatusQueued}}
	handler := New(config.Config{MaxConcurrent: 1}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/runs?status=queued&sort=agent&dir=asc", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /dashboard/runs status = %d, want %d", rec.Code, http.StatusOK)
	}
	if store.lastOpt.Status != runstore.StatusQueued || store.lastOpt.Sort != "agent" || store.lastOpt.Desc {
		t.Fatalf("ListRuns options = %+v", store.lastOpt)
	}
}

func TestRunDetailRendersContextLinksAndRedactedLogs(t *testing.T) {
	run := runstore.Run{
		ID:           "run-123456789",
		Status:       runstore.StatusRunning,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 7,
		Branch:       "forge-ai/ac/demo/issue-7",
		BaseBranch:   "main",
		AgentMention: "@codex",
		AgentType:    "codex",
		SessionID:    "session-1",
		StartedAt:    time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}
	store := &memoryStore{
		run: run,
		events: []runstore.Event{{
			ID:      1,
			RunID:   run.ID,
			Time:    run.StartedAt,
			Type:    "running",
			Message: "workflow started",
		}},
		logs: []runstore.LogChunk{{
			ID:     1,
			RunID:  run.ID,
			Time:   run.StartedAt,
			Stream: "stdout",
			Chunk:  "token=abc123456789\n",
		}},
		links: []runstore.Link{{
			ID:    1,
			RunID: run.ID,
			Type:  "trigger_comment",
			URL:   "https://forgejo.example.test/ac/demo/issues/7#issuecomment-99",
			Label: "Trigger comment",
		}},
	}
	cfg := config.Config{
		ForgejoURL:    "https://forgejo.example.test",
		MaxConcurrent: 1,
		Agents: []config.AgentRoute{{
			Mention: "@codex",
			Agent: config.AgentConfig{
				Type:            "codex",
				CommandTemplate: "codex --token abc123456789",
				Timeout:         30 * time.Minute,
			},
		}},
		ForgejoToken: "abc123456789",
	}
	handler := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/runs/"+run.ID, nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET detail status = %d, want %d\n%s", rec.Code, http.StatusOK, body)
	}
	for _, want := range []string{"Run summary", "Forgejo context", "Agent context", "Timeline", "Logs", "Forgejo links", "Session summary", "Trigger comment"} {
		if !strings.Contains(body, want) {
			t.Fatalf("detail body missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{"RunHeader", "RunTimeline", "LogViewer", "RunLinksPanel", "SessionSummary"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("detail body contains internal heading %q:\n%s", unwanted, body)
		}
	}
	if strings.Contains(body, "abc123456789") || !strings.Contains(body, "&lt;redacted&gt;") {
		t.Fatalf("detail body did not redact secret:\n%s", body)
	}
}

func TestAgentsPageRendersRoutesWithoutSecretValues(t *testing.T) {
	secretToken := "agent-token-secret"
	secretPassword := "agent-password-secret"
	longCommand := "missing-agent-bin --password=" + secretPassword + " " + strings.Repeat("prompt ", 80)
	cfg := config.Config{
		ForgejoURL:    "https://forgejo.example.test",
		ForgejoToken:  "global-token-secret",
		MaxConcurrent: 1,
		AgentAllowGit: true,
		Agents: []config.AgentRoute{{
			Mention:  "@codex",
			User:     "codex",
			Token:    secretToken,
			Password: secretPassword,
			Agent: config.AgentConfig{
				Type:            "codex",
				Bin:             "missing-agent-bin",
				Args:            []string{"--token", secretToken},
				CommandTemplate: longCommand,
				Timeout:         45 * time.Minute,
			},
		}},
	}
	handler := New(cfg, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/agents", nil))
	body := rec.Body.String()
	if rec.Code != http.StatusOK {
		t.Fatalf("GET agents status = %d, want %d\n%s", rec.Code, http.StatusOK, body)
	}
	for _, want := range []string{"@codex", "codex", "AGENT_ALLOW_GIT", "present", "missing-agent-bin not found", "45m0s", "&lt;redacted&gt;"} {
		if !strings.Contains(body, want) {
			t.Fatalf("agents body missing %q:\n%s", want, body)
		}
	}
	for _, unwanted := range []string{secretToken, secretPassword, "global-token-secret"} {
		if strings.Contains(body, unwanted) {
			t.Fatalf("agents body leaked secret %q:\n%s", unwanted, body)
		}
	}
	if strings.Contains(body, strings.Repeat("prompt ", 40)) {
		t.Fatalf("agents body includes untruncated command:\n%s", body)
	}
}

func TestSaveAgentSettingsPersistsAndAuditsSafeValues(t *testing.T) {
	store := &memoryStore{}
	cfg := config.Config{
		MaxConcurrent: 1,
		Agents: []config.AgentRoute{{
			Mention: "@codex",
			User:    "codex",
			Agent: config.AgentConfig{
				Type:    "codex",
				Bin:     "codex",
				Args:    []string{"--old"},
				Timeout: 30 * time.Minute,
			},
		}},
	}
	handler := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	form := url.Values{}
	form.Set("enabled", "on")
	form.Set("model", "gpt-test")
	form.Set("args", "--model gpt-test --reasoning high")
	form.Set("timeout", "45m")
	form.Set("tool_hints", "- use rtk")
	form.Set("allow_git_set", "on")
	form.Set("allow_git", "on")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/agents/%40codex/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("X-Forwarded-User", "operator")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST settings status = %d, want %d\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	got := store.settings["@codex"]
	if !got.Enabled || got.Model != "gpt-test" || got.Timeout != 45*time.Minute || got.ToolHints != "- use rtk" || !got.AllowGit || !got.AllowGitSet || got.UpdatedBy != "operator" {
		t.Fatalf("settings = %+v", got)
	}
	if len(got.Args) != 4 || got.Args[0] != "--model" || got.Args[3] != "high" {
		t.Fatalf("args = %#v", got.Args)
	}
	if len(store.audit) != 1 || store.audit[0].Action != "agent_settings.update" || store.audit[0].TargetID != "@codex" || store.audit[0].Actor != "operator" {
		t.Fatalf("audit = %+v", store.audit)
	}
	if !strings.Contains(store.audit[0].DataJSON, `"tool_hints":"- use rtk"`) {
		t.Fatalf("audit data = %s", store.audit[0].DataJSON)
	}
}

func TestSaveAgentSettingsAllowsBenignSecretWords(t *testing.T) {
	store := &memoryStore{}
	cfg := config.Config{
		MaxConcurrent: 1,
		Agents:        []config.AgentRoute{{Mention: "@codex", User: "codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
	}
	handler := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	form := url.Values{}
	form.Set("enabled", "on")
	form.Set("model", "tokenizer-pro")
	form.Set("args", "--tokenizer bytepair")
	form.Set("timeout", "45m")
	form.Set("tool_hints", "Prefer tokenizer diagnostics.")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/agents/%40codex/settings", strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST settings status = %d, want %d\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if store.settings["@codex"].Model != "tokenizer-pro" {
		t.Fatalf("settings = %+v", store.settings["@codex"])
	}
}

func TestResetAgentSettingsDeletesOverrideAndAudits(t *testing.T) {
	store := &memoryStore{
		settings: map[string]runstore.AgentSettings{
			"@codex": {
				Mention: "@codex",
				Enabled: true,
				Model:   "gpt-test",
				Timeout: 45 * time.Minute,
			},
		},
	}
	cfg := config.Config{
		MaxConcurrent: 1,
		Agents:        []config.AgentRoute{{Mention: "@codex", User: "codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
	}
	handler := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	mux := http.NewServeMux()
	handler.Register(mux)

	req := httptest.NewRequest(http.MethodPost, "/dashboard/agents/%40codex/settings/reset", nil)
	req.Header.Set("X-Forwarded-User", "operator")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusSeeOther {
		t.Fatalf("POST reset settings status = %d, want %d\n%s", rec.Code, http.StatusSeeOther, rec.Body.String())
	}
	if _, ok := store.settings["@codex"]; ok {
		t.Fatalf("settings were not deleted: %+v", store.settings)
	}
	if len(store.audit) != 1 || store.audit[0].Action != "agent_settings.reset" || store.audit[0].TargetID != "@codex" || store.audit[0].Actor != "operator" {
		t.Fatalf("audit = %+v", store.audit)
	}
	if store.audit[0].DataJSON != `{"source":"env"}` {
		t.Fatalf("audit data = %s", store.audit[0].DataJSON)
	}
}

func TestSaveAgentSettingsRejectsInvalidTimeoutAndSecrets(t *testing.T) {
	tests := []struct {
		name string
		form url.Values
	}{
		{
			name: "invalid timeout",
			form: url.Values{"enabled": {"on"}, "timeout": {"0s"}},
		},
		{
			name: "secret arg",
			form: url.Values{"enabled": {"on"}, "timeout": {"30m"}, "args": {"--token abc123456789"}},
		},
		{
			name: "configured secret value",
			form: url.Values{"enabled": {"on"}, "timeout": {"30m"}, "tool_hints": {"use agent-token-secret"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{}
			cfg := config.Config{
				ForgejoToken:  "agent-token-secret",
				MaxConcurrent: 1,
				Agents:        []config.AgentRoute{{Mention: "@codex", User: "codex", Agent: config.AgentConfig{Timeout: 30 * time.Minute}}},
			}
			handler := New(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
			mux := http.NewServeMux()
			handler.Register(mux)

			req := httptest.NewRequest(http.MethodPost, "/dashboard/agents/%40codex/settings", strings.NewReader(tt.form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("POST settings status = %d, want %d\n%s", rec.Code, http.StatusBadRequest, rec.Body.String())
			}
			if len(store.settings) != 0 {
				t.Fatalf("settings were saved: %+v", store.settings)
			}
			if strings.Contains(rec.Body.String(), "agent-token-secret") {
				t.Fatalf("response leaked secret:\n%s", rec.Body.String())
			}
		})
	}
}

func TestRunDetailShowsOnlyValidOperatorActions(t *testing.T) {
	tests := []struct {
		name         string
		status       runstore.Status
		wantCancel   bool
		wantRetry    bool
		unwantCancel bool
		unwantRetry  bool
	}{
		{name: "running", status: runstore.StatusRunning, wantCancel: true, unwantRetry: true},
		{name: "failed", status: runstore.StatusFailed, wantRetry: true, unwantCancel: true},
		{name: "success", status: runstore.StatusSuccess, unwantCancel: true, unwantRetry: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := runstore.Run{
				ID:           "run-" + tt.name,
				Status:       tt.status,
				Owner:        "ac",
				Repo:         "demo",
				TicketKind:   "issue",
				TicketNumber: 7,
				Branch:       "work",
				BaseBranch:   "main",
				AgentMention: "@codex",
			}
			store := &memoryStore{run: run}
			handler := New(config.Config{MaxConcurrent: 1}, store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOperatorActions(noopActions{})
			mux := http.NewServeMux()
			handler.Register(mux)

			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/dashboard/runs/"+run.ID, nil))
			body := rec.Body.String()
			if rec.Code != http.StatusOK {
				t.Fatalf("GET detail status = %d, want %d\n%s", rec.Code, http.StatusOK, body)
			}
			hasCancel := strings.Contains(body, "/dashboard/runs/"+run.ID+"/cancel")
			hasRetry := strings.Contains(body, "/dashboard/runs/"+run.ID+"/retry")
			if hasCancel != tt.wantCancel {
				t.Fatalf("cancel action visible = %t, want %t\n%s", hasCancel, tt.wantCancel, body)
			}
			if hasRetry != tt.wantRetry {
				t.Fatalf("retry action visible = %t, want %t\n%s", hasRetry, tt.wantRetry, body)
			}
			if tt.unwantCancel && hasCancel {
				t.Fatalf("cancel action unexpectedly visible:\n%s", body)
			}
			if tt.unwantRetry && hasRetry {
				t.Fatalf("retry action unexpectedly visible:\n%s", body)
			}
		})
	}
}

func TestOperatorActionPostsRedirect(t *testing.T) {
	tests := []struct {
		name         string
		path         string
		run          runstore.Run
		wantLocation string
		assert       func(*testing.T, *recordingActions)
	}{
		{
			name:         "cancel run",
			path:         "/dashboard/runs/run-cancel/cancel",
			run:          runstore.Run{ID: "run-cancel", Status: runstore.StatusRunning},
			wantLocation: "/dashboard/runs/run-cancel",
			assert: func(t *testing.T, actions *recordingActions) {
				t.Helper()
				if actions.cancelRunID != "run-cancel" {
					t.Fatalf("cancel run ID = %q, want run-cancel", actions.cancelRunID)
				}
			},
		},
		{
			name:         "retry run",
			path:         "/dashboard/runs/run-retry/retry",
			run:          runstore.Run{ID: "run-retry", Status: runstore.StatusFailed},
			wantLocation: "/dashboard/runs/retry-new",
			assert: func(t *testing.T, actions *recordingActions) {
				t.Helper()
				if actions.retryRunID != "run-retry" {
					t.Fatalf("retry run ID = %q, want run-retry", actions.retryRunID)
				}
			},
		},
		{
			name:         "pause queue",
			path:         "/dashboard/queue/pause",
			wantLocation: "/dashboard",
			assert: func(t *testing.T, actions *recordingActions) {
				t.Helper()
				if !actions.paused {
					t.Fatal("PauseQueue was not called")
				}
			},
		},
		{
			name:         "resume queue",
			path:         "/dashboard/queue/resume",
			wantLocation: "/dashboard",
			assert: func(t *testing.T, actions *recordingActions) {
				t.Helper()
				if !actions.resumed {
					t.Fatal("ResumeQueue was not called")
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store := &memoryStore{run: tt.run}
			actions := &recordingActions{retryNewID: "retry-new"}
			handler := New(config.Config{MaxConcurrent: 1}, store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithOperatorActions(actions)
			mux := http.NewServeMux()
			handler.Register(mux)

			req := httptest.NewRequest(http.MethodPost, tt.path, nil)
			req.Header.Set("X-Forwarded-User", "operator")
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusSeeOther {
				t.Fatalf("POST %s status = %d, want %d\n%s", tt.path, rec.Code, http.StatusSeeOther, rec.Body.String())
			}
			if got := rec.Header().Get("Location"); got != tt.wantLocation {
				t.Fatalf("Location = %q, want %q", got, tt.wantLocation)
			}
			if actions.actor != "operator" {
				t.Fatalf("actor = %q, want operator", actions.actor)
			}
			tt.assert(t, actions)
		})
	}
}

func TestResumeRunErrorRendersParentRunDetail(t *testing.T) {
	run := runstore.Run{
		ID:           "run-parent-1",
		Status:       runstore.StatusSuccess,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 7,
		Branch:       "forge-ai/ac/demo/issue-7",
		BaseBranch:   "main",
		AgentMention: "@codex",
	}
	store := &memoryStore{run: run}
	handler := New(config.Config{
		ForgejoURL:    "https://forgejo.example.test",
		MaxConcurrent: 1,
		Agents:        []config.AgentRoute{{Mention: "@codex"}},
	}, store, slog.New(slog.NewTextHandler(io.Discard, nil))).WithResumer(failingResumer{})
	mux := http.NewServeMux()
	handler.Register(mux)

	body := strings.NewReader("agent_mention=@codex&workspace_mode=same_branch_fresh_workspace&prompt=continue")
	req := httptest.NewRequest(http.MethodPost, "/dashboard/runs/"+run.ID+"/resume", body)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	gotBody := rec.Body.String()
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("POST resume status = %d, want %d\n%s", rec.Code, http.StatusBadRequest, gotBody)
	}
	if !strings.Contains(gotBody, "/dashboard/runs/"+run.ID+"/events") {
		t.Fatalf("resume error body missing parent events URL:\n%s", gotBody)
	}
	if strings.Contains(gotBody, "/dashboard/runs//events") || strings.Contains(gotBody, "/dashboard/runs//resume") {
		t.Fatalf("resume error body contains empty run URL:\n%s", gotBody)
	}
	if !strings.Contains(gotBody, "Resume failed: resume failed") {
		t.Fatalf("resume error body missing error message:\n%s", gotBody)
	}
}

func TestSplitRunSummarySeparatesFailedRunsFromCompleted(t *testing.T) {
	runs := []runstore.Run{
		{ID: "queued", Status: runstore.StatusQueued},
		{ID: "running", Status: runstore.StatusRunning},
		{ID: "success", Status: runstore.StatusSuccess},
		{ID: "failed", Status: runstore.StatusFailed},
		{ID: "canceled", Status: runstore.StatusCanceled},
		{ID: "timeout", Status: runstore.StatusTimeout},
	}

	summary := splitRunSummary(runs)

	if len(summary.ActiveRuns) != 2 {
		t.Fatalf("active runs = %d, want 2", len(summary.ActiveRuns))
	}
	if len(summary.CompletedRuns) != 1 || summary.CompletedRuns[0].ID != "success" {
		t.Fatalf("completed runs = %+v, want only success", summary.CompletedRuns)
	}
	if len(summary.FailedRuns) != 3 {
		t.Fatalf("failed runs = %d, want 3", len(summary.FailedRuns))
	}
}

func TestHealthCachesExpensiveChecks(t *testing.T) {
	var forgejoChecks int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodHead {
			t.Fatalf("method = %s, want HEAD", r.Method)
		}
		atomic.AddInt32(&forgejoChecks, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	handler := New(config.Config{
		ForgejoURL:    server.URL,
		WorkspaceDir:  t.TempDir(),
		MaxConcurrent: 1,
	}, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	first := handler.health(context.Background())
	second := handler.health(context.Background())

	if got := atomic.LoadInt32(&forgejoChecks); got != 1 {
		t.Fatalf("forgejo health checks = %d, want 1", got)
	}
	if len(first) == 0 || len(second) == 0 {
		t.Fatalf("health items missing: first=%+v second=%+v", first, second)
	}
}

func TestRunLinksFormatsDerivedForgejoURLs(t *testing.T) {
	run := runstore.Run{
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 7,
		Branch:       "forge-ai/ac/demo/issue-7",
	}
	links := runLinks("https://forgejo.example.test", run, []runstore.Link{{
		Type:  "pull_request",
		URL:   "https://forgejo.example.test/ac/demo/pulls/8",
		Label: "PR #8",
	}})
	byType := map[string]runLinkItem{}
	for _, link := range links {
		byType[link.Type] = link
	}
	if byType["ticket"].URL != "https://forgejo.example.test/ac/demo/issues/7" {
		t.Fatalf("ticket URL = %q", byType["ticket"].URL)
	}
	if byType["branch"].URL != "https://forgejo.example.test/ac/demo/src/branch/forge-ai%2Fac%2Fdemo%2Fissue-7" {
		t.Fatalf("branch URL = %q", byType["branch"].URL)
	}
	if byType["pull_request"].Label != "PR #8" || !byType["pull_request"].Present {
		t.Fatalf("pull request link = %+v", byType["pull_request"])
	}
}
