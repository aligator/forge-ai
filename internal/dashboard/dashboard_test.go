package dashboard

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
	"codeberg.org/forge-ai/internal/runstore"
)

type memoryStore struct {
	run    runstore.Run
	events []runstore.Event
	logs   []runstore.LogChunk
}

func (s memoryStore) ListRuns(context.Context, runstore.ListRunsOptions) ([]runstore.Run, error) {
	return []runstore.Run{s.run}, nil
}

func (s memoryStore) GetRun(context.Context, string) (runstore.Run, error) {
	return s.run, nil
}

func (s memoryStore) ListEvents(context.Context, string) ([]runstore.Event, error) {
	return s.events, nil
}

func (s memoryStore) ListEventsSince(_ context.Context, _ string, sinceID int64) ([]runstore.Event, error) {
	return filterSince(s.events, sinceID, func(e runstore.Event) int64 { return e.ID }), nil
}

func (s memoryStore) ListLogChunks(context.Context, string) ([]runstore.LogChunk, error) {
	return s.logs, nil
}

func (s memoryStore) ListLogChunksSince(_ context.Context, _ string, sinceID int64) ([]runstore.LogChunk, error) {
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

func (s memoryStore) ListLinks(context.Context, string) ([]runstore.Link, error) {
	return nil, nil
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
	handler := New(config.Config{ForgejoURL: "https://forgejo.example.test", MaxConcurrent: 1}, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
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
