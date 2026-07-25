package server

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
)

type stubWorkflow struct{}

func (stubWorkflow) Handle(context.Context, string, forgejo.WebhookPayload) error {
	return nil
}

type stubDashboardStore struct {
	runs []runstore.Run
}

func (s stubDashboardStore) ListRuns(context.Context, runstore.ListRunsOptions) ([]runstore.Run, error) {
	return s.runs, nil
}

func (s stubDashboardStore) GetRun(_ context.Context, id string) (runstore.Run, error) {
	for _, run := range s.runs {
		if run.ID == id {
			return run, nil
		}
	}
	return runstore.Run{}, nil
}

func (s stubDashboardStore) ListEvents(context.Context, string) ([]runstore.Event, error) {
	return nil, nil
}

func (s stubDashboardStore) ListEventsSince(context.Context, string, int64) ([]runstore.Event, error) {
	return nil, nil
}

func (s stubDashboardStore) ListLogChunks(context.Context, string) ([]runstore.LogChunk, error) {
	return nil, nil
}

func (s stubDashboardStore) ListLogChunksSince(context.Context, string, int64) ([]runstore.LogChunk, error) {
	return nil, nil
}

func (s stubDashboardStore) ListLinks(context.Context, string) ([]runstore.Link, error) {
	return nil, nil
}

func (s stubDashboardStore) ListAuditEvents(context.Context, runstore.ListAuditEventsOptions) ([]runstore.AuditEvent, error) {
	return nil, nil
}

func (s stubDashboardStore) AddAuditEvent(context.Context, runstore.AuditEventInput) error {
	return nil
}

func (s stubDashboardStore) GetAgentSettings(context.Context, string) (runstore.AgentSettings, error) {
	return runstore.AgentSettings{}, runstore.ErrAgentSettingsNotFound
}

func (s stubDashboardStore) UpsertAgentSettings(context.Context, runstore.UpsertAgentSettingsInput) (runstore.AgentSettings, error) {
	return runstore.AgentSettings{}, nil
}

func (s stubDashboardStore) DeleteAgentSettings(context.Context, string) error {
	return nil
}

func TestNewRegistersDashboardWithoutDisturbingExistingRoutes(t *testing.T) {
	handler := New(config.Config{
		ForgejoURL:    "https://forgejo.example.test",
		MaxConcurrent: 2,
		CreatePR:      true,
	}, stubWorkflow{}, stubDashboardStore{runs: []runstore.Run{{
		ID:           "run-1",
		Status:       runstore.StatusRunning,
		Owner:        "ac",
		Repo:         "demo",
		TicketKind:   "issue",
		TicketNumber: 6,
		Branch:       "forge-ai/ac/demo/issue-6",
		BaseBranch:   "main",
		AgentMention: "@codex",
		AgentType:    "codex",
		StartedAt:    time.Date(2026, 7, 5, 12, 0, 0, 0, time.UTC),
	}}}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	health := httptest.NewRecorder()
	handler.ServeHTTP(health, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusNoContent {
		t.Fatalf("GET /healthz status = %d, want %d", health.Code, http.StatusNoContent)
	}

	webhook := httptest.NewRecorder()
	handler.ServeHTTP(webhook, httptest.NewRequest(http.MethodPost, "/webhook", strings.NewReader(`{}`)))
	if webhook.Code != http.StatusAccepted {
		t.Fatalf("POST /webhook status = %d, want %d", webhook.Code, http.StatusAccepted)
	}

	dashboard := httptest.NewRecorder()
	handler.ServeHTTP(dashboard, httptest.NewRequest(http.MethodGet, "/dashboard", nil))
	if dashboard.Code != http.StatusOK {
		t.Fatalf("GET /dashboard status = %d, want %d", dashboard.Code, http.StatusOK)
	}
	body := dashboard.Body.String()
	if !strings.Contains(body, "Operations") || !strings.Contains(body, "run-1") || !strings.Contains(body, "/dashboard/assets/app.css") {
		t.Fatalf("dashboard body missing expected content:\n%s", body)
	}

	root := httptest.NewRecorder()
	handler.ServeHTTP(root, httptest.NewRequest(http.MethodGet, "/", nil))
	if root.Code != http.StatusSeeOther {
		t.Fatalf("GET / status = %d, want %d", root.Code, http.StatusSeeOther)
	}
	if location := root.Header().Get("Location"); location != "/dashboard" {
		t.Fatalf("GET / redirect location = %q, want /dashboard", location)
	}

	dashboardSlash := httptest.NewRecorder()
	handler.ServeHTTP(dashboardSlash, httptest.NewRequest(http.MethodGet, "/dashboard/", nil))
	if dashboardSlash.Code != http.StatusSeeOther {
		t.Fatalf("GET /dashboard/ status = %d, want %d", dashboardSlash.Code, http.StatusSeeOther)
	}
	if location := dashboardSlash.Header().Get("Location"); location != "/dashboard" {
		t.Fatalf("GET /dashboard/ redirect location = %q, want /dashboard", location)
	}
}
