package service

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

func TestServiceRestoresPersistedMaxConcurrent(t *testing.T) {
	ctx := context.Background()
	store, err := runstore.OpenSQLite(ctx, filepath.Join(t.TempDir(), "runstore.sqlite"))
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	defer store.Close()

	cfg := config.Config{MaxConcurrent: 1}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	svc := New(Options{Config: cfg, RunStore: store, Logger: logger})
	if got := svc.RuntimeSnapshot().MaxConcurrent; got != 1 {
		t.Fatalf("initial MaxConcurrent = %d, want 1", got)
	}
	if err := svc.SetMaxConcurrent(ctx, 4, "alice"); err != nil {
		t.Fatalf("SetMaxConcurrent() error = %v", err)
	}

	// Simulate a restart: a fresh Service backed by the same store must pick up
	// the persisted value instead of the config default.
	restarted := New(Options{Config: cfg, RunStore: store, Logger: logger})
	if got := restarted.RuntimeSnapshot().MaxConcurrent; got != 4 {
		t.Fatalf("restored MaxConcurrent = %d, want 4", got)
	}
}
