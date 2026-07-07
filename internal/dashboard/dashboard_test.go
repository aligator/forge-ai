package dashboard

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

type memoryStore struct {
	run     runstore.Run
	runs    []runstore.Run
	events  []runstore.Event
	logs    []runstore.LogChunk
	links   []runstore.Link
	lastOpt runstore.ListRunsOptions
}

type failingResumer struct {
	cancelOK bool
}

func (r failingResumer) ManualResume(context.Context, string, string, string, string, string, string) (string, error) {
	return "", errors.New("resume failed")
}

func (r failingResumer) CancelRun(string) bool {
	return r.cancelOK
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
