package runstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func OpenSQLite(ctx context.Context, path string) (*SQLiteStore, error) {
	if path == "" {
		return nil, errors.New("runstore path is required")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create runstore dir: %w", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite runstore: %w", err)
	}
	db.SetMaxOpenConns(1)
	store := &SQLiteStore{db: db}
	if err := store.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

func (s *SQLiteStore) migrate(ctx context.Context) error {
	statements := []string{
		`PRAGMA foreign_keys = ON`,
		`CREATE TABLE IF NOT EXISTS runs (
			id TEXT PRIMARY KEY,
			kind TEXT NOT NULL CHECK (kind IN ('webhook_run', 'manual_resume')),
			status TEXT NOT NULL CHECK (status IN ('queued', 'running', 'success', 'failed', 'canceled', 'timeout')),
			owner TEXT NOT NULL,
			repo TEXT NOT NULL,
			ticket_kind TEXT NOT NULL,
			ticket_number INTEGER NOT NULL,
			branch TEXT NOT NULL,
			base_branch TEXT NOT NULL,
			agent_mention TEXT NOT NULL,
			agent_type TEXT NOT NULL,
			session_id TEXT NOT NULL DEFAULT '',
			parent_run_id TEXT NOT NULL DEFAULT '',
			started_at TEXT NOT NULL,
			finished_at TEXT,
			error TEXT NOT NULL DEFAULT '',
			created_by TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS run_events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			ts TEXT NOT NULL,
			type TEXT NOT NULL,
			message TEXT NOT NULL DEFAULT '',
			data_json TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE TABLE IF NOT EXISTS run_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			ts TEXT NOT NULL,
			stream TEXT NOT NULL,
			chunk TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS run_links (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			run_id TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			url TEXT NOT NULL,
			label TEXT NOT NULL DEFAULT ''
		)`,
		`CREATE INDEX IF NOT EXISTS idx_runs_ticket ON runs(owner, repo, ticket_kind, ticket_number)`,
		`CREATE INDEX IF NOT EXISTS idx_run_events_run_id ON run_events(run_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_run_logs_run_id ON run_logs(run_id, id)`,
		`CREATE INDEX IF NOT EXISTS idx_run_links_run_id ON run_links(run_id, id)`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("migrate runstore: %w", err)
		}
	}
	return nil
}

func (s *SQLiteStore) CreateRun(ctx context.Context, in CreateRunInput) (Run, error) {
	if in.ID == "" {
		in.ID = uuid.NewString()
	}
	if in.Kind == "" {
		in.Kind = RunKindWebhookRun
	}
	if in.Status == "" {
		in.Status = StatusQueued
	}
	if in.StartedAt.IsZero() {
		in.StartedAt = time.Now().UTC()
	}
	run := Run{
		ID:           in.ID,
		Kind:         in.Kind,
		Status:       in.Status,
		Owner:        in.Owner,
		Repo:         in.Repo,
		TicketKind:   in.TicketKind,
		TicketNumber: in.TicketNumber,
		Branch:       in.Branch,
		BaseBranch:   in.BaseBranch,
		AgentMention: in.AgentMention,
		AgentType:    in.AgentType,
		SessionID:    in.SessionID,
		ParentRunID:  in.ParentRunID,
		StartedAt:    in.StartedAt.UTC(),
		CreatedBy:    in.CreatedBy,
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO runs (
		id, kind, status, owner, repo, ticket_kind, ticket_number, branch, base_branch,
		agent_mention, agent_type, session_id, parent_run_id, started_at, error, created_by
	) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '', ?)`,
		run.ID, run.Kind, run.Status, run.Owner, run.Repo, run.TicketKind, run.TicketNumber, run.Branch, run.BaseBranch,
		run.AgentMention, run.AgentType, run.SessionID, run.ParentRunID, formatTime(run.StartedAt), run.CreatedBy)
	if err != nil {
		return Run{}, fmt.Errorf("create run: %w", err)
	}
	return run, nil
}

func (s *SQLiteStore) UpdateRunStatus(ctx context.Context, id string, status Status, finishedAt time.Time, message string) error {
	var finished any
	if !finishedAt.IsZero() {
		finished = formatTime(finishedAt.UTC())
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin status update: %w", err)
	}
	defer tx.Rollback()

	var current Status
	if err := tx.QueryRowContext(ctx, `SELECT status FROM runs WHERE id = ?`, id).Scan(&current); err != nil {
		return err
	}
	if err := validateStatusTransition(current, status); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, `UPDATE runs SET status = ?, finished_at = ?, error = ? WHERE id = ?`, status, finished, message, id)
	if err != nil {
		return fmt.Errorf("update run status: %w", err)
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit status update: %w", err)
	}
	return nil
}

func (s *SQLiteStore) SetSessionID(ctx context.Context, id, sessionID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE runs SET session_id = ? WHERE id = ?`, sessionID, id)
	if err != nil {
		return fmt.Errorf("set session id: %w", err)
	}
	return nil
}

func (s *SQLiteStore) AddEvent(ctx context.Context, in EventInput) error {
	if in.Time.IsZero() {
		in.Time = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_events (run_id, ts, type, message, data_json) VALUES (?, ?, ?, ?, ?)`,
		in.RunID, formatTime(in.Time), in.Type, in.Message, in.DataJSON)
	if err != nil {
		return fmt.Errorf("add run event: %w", err)
	}
	return nil
}

func (s *SQLiteStore) AddLogChunk(ctx context.Context, in LogChunkInput) error {
	if in.Time.IsZero() {
		in.Time = time.Now().UTC()
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_logs (run_id, ts, stream, chunk) VALUES (?, ?, ?, ?)`,
		in.RunID, formatTime(in.Time), in.Stream, in.Chunk)
	if err != nil {
		return fmt.Errorf("add run log chunk: %w", err)
	}
	return nil
}

func (s *SQLiteStore) AddLink(ctx context.Context, in LinkInput) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO run_links (run_id, type, url, label) VALUES (?, ?, ?, ?)`,
		in.RunID, in.Type, in.URL, in.Label)
	if err != nil {
		return fmt.Errorf("add run link: %w", err)
	}
	return nil
}

func (s *SQLiteStore) GetRun(ctx context.Context, id string) (Run, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, kind, status, owner, repo, ticket_kind, ticket_number, branch, base_branch,
		agent_mention, agent_type, session_id, parent_run_id, started_at, finished_at, error, created_by
		FROM runs WHERE id = ?`, id)
	return scanRun(row)
}

func (s *SQLiteStore) ListRuns(ctx context.Context, opts ListRunsOptions) ([]Run, error) {
	limit := opts.Limit
	if limit <= 0 || limit > 100 {
		limit = 50
	}

	query := `SELECT id, kind, status, owner, repo, ticket_kind, ticket_number, branch, base_branch,
		agent_mention, agent_type, session_id, parent_run_id, started_at, finished_at, error, created_by
		FROM runs`
	args := []any{}
	if opts.Status != "" {
		query += ` WHERE status = ?`
		args = append(args, opts.Status)
	}
	query += ` ORDER BY started_at DESC, id DESC LIMIT ?`
	args = append(args, limit)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		run, err := scanRun(rows)
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *SQLiteStore) ListEvents(ctx context.Context, runID string) ([]Event, error) {
	return s.ListEventsSince(ctx, runID, 0)
}

func (s *SQLiteStore) ListEventsSince(ctx context.Context, runID string, sinceID int64) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, ts, type, message, data_json FROM run_events WHERE run_id = ? AND id > ? ORDER BY id`, runID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("list run events: %w", err)
	}
	defer rows.Close()
	var events []Event
	for rows.Next() {
		var e Event
		var ts string
		if err := rows.Scan(&e.ID, &e.RunID, &ts, &e.Type, &e.Message, &e.DataJSON); err != nil {
			return nil, err
		}
		e.Time = parseTime(ts)
		events = append(events, e)
	}
	return events, rows.Err()
}

func (s *SQLiteStore) ListLogChunks(ctx context.Context, runID string) ([]LogChunk, error) {
	return s.ListLogChunksSince(ctx, runID, 0)
}

func (s *SQLiteStore) ListLogChunksSince(ctx context.Context, runID string, sinceID int64) ([]LogChunk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, ts, stream, chunk FROM run_logs WHERE run_id = ? AND id > ? ORDER BY id`, runID, sinceID)
	if err != nil {
		return nil, fmt.Errorf("list run log chunks: %w", err)
	}
	defer rows.Close()
	var chunks []LogChunk
	for rows.Next() {
		var c LogChunk
		var ts string
		if err := rows.Scan(&c.ID, &c.RunID, &ts, &c.Stream, &c.Chunk); err != nil {
			return nil, err
		}
		c.Time = parseTime(ts)
		chunks = append(chunks, c)
	}
	return chunks, rows.Err()
}

func (s *SQLiteStore) ListLinks(ctx context.Context, runID string) ([]Link, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, run_id, type, url, label FROM run_links WHERE run_id = ? ORDER BY id`, runID)
	if err != nil {
		return nil, fmt.Errorf("list run links: %w", err)
	}
	defer rows.Close()
	var links []Link
	for rows.Next() {
		var l Link
		if err := rows.Scan(&l.ID, &l.RunID, &l.Type, &l.URL, &l.Label); err != nil {
			return nil, err
		}
		links = append(links, l)
	}
	return links, rows.Err()
}

type runScanner interface {
	Scan(...any) error
}

func scanRun(row runScanner) (Run, error) {
	var run Run
	var startedAt string
	var finishedAt sql.NullString
	if err := row.Scan(&run.ID, &run.Kind, &run.Status, &run.Owner, &run.Repo, &run.TicketKind, &run.TicketNumber, &run.Branch, &run.BaseBranch,
		&run.AgentMention, &run.AgentType, &run.SessionID, &run.ParentRunID, &startedAt, &finishedAt, &run.Error, &run.CreatedBy); err != nil {
		return Run{}, err
	}
	run.StartedAt = parseTime(startedAt)
	if finishedAt.Valid {
		run.FinishedAt = parseTime(finishedAt.String)
	}
	return run, nil
}

func formatTime(t time.Time) string {
	return t.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) time.Time {
	t, _ := time.Parse(time.RFC3339Nano, value)
	return t
}
