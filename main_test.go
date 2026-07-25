package main

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
)

type fakeAgentSettingsStore struct {
	settings runstore.AgentSettings
	err      error
}

func (s fakeAgentSettingsStore) GetAgentSettings(context.Context, string) (runstore.AgentSettings, error) {
	if s.err != nil {
		return runstore.AgentSettings{}, s.err
	}
	return s.settings, nil
}

func TestStoredSettingsAgentUsesLatestTimeout(t *testing.T) {
	ag := &storedSettingsAgent{
		mention: "@codex",
		base: config.AgentConfig{
			CommandTemplate: "sleep 5",
			Timeout:         time.Hour,
		},
		store: fakeAgentSettingsStore{
			settings: runstore.AgentSettings{
				Mention: "@codex",
				Enabled: true,
				Timeout: 20 * time.Millisecond,
			},
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	start := time.Now()
	_, err := ag.Run(context.Background(), t.TempDir(), "prompt", "")
	if err == nil {
		t.Fatal("Run() error = nil, want timeout error")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("Run() elapsed = %s, want stored timeout to cancel promptly", elapsed)
	}
}

func TestStoredSettingsAgentFallsBackToBaseWhenSettingsReset(t *testing.T) {
	ag := &storedSettingsAgent{
		mention: "@codex",
		base: config.AgentConfig{
			CommandTemplate: "printf done",
			Timeout:         time.Second,
		},
		store:  fakeAgentSettingsStore{err: runstore.ErrAgentSettingsNotFound},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	result, err := ag.Run(context.Background(), t.TempDir(), "prompt", "")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("Output = %q, want done", result.Output)
	}
}

func TestStoredSettingsAgentIgnoresMissingSettingsOnly(t *testing.T) {
	ag := &storedSettingsAgent{
		mention: "@codex",
		base: config.AgentConfig{
			CommandTemplate: "printf done",
			Timeout:         time.Second,
		},
		store:  fakeAgentSettingsStore{err: errors.New("database closed")},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	result, err := ag.Run(context.Background(), t.TempDir(), "prompt", "")
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Output != "done" {
		t.Fatalf("Output = %q, want done", result.Output)
	}
}
