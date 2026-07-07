package runstore

import (
	"context"
	"errors"
	"fmt"
	"time"
)

var ErrInvalidStatusTransition = errors.New("invalid run status transition")

type RunKind string

const (
	RunKindWebhookRun   RunKind = "webhook_run"
	RunKindManualResume RunKind = "manual_resume"
)

type Status string

const (
	StatusQueued   Status = "queued"
	StatusRunning  Status = "running"
	StatusSuccess  Status = "success"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
	StatusTimeout  Status = "timeout"
)

type RunStore interface {
	CreateRun(context.Context, CreateRunInput) (Run, error)
	GetRun(context.Context, string) (Run, error)
	UpdateRunStatus(context.Context, string, Status, time.Time, string) error
	SetSessionID(context.Context, string, string) error
	AddEvent(context.Context, EventInput) error
	AddLogChunk(context.Context, LogChunkInput) error
	AddLink(context.Context, LinkInput) error
	AddAuditEvent(context.Context, AuditEventInput) error
}

type ListRunsOptions struct {
	Limit  int
	Status Status
	Sort   string
	Desc   bool
}

type Run struct {
	ID           string
	Kind         RunKind
	Status       Status
	Owner        string
	Repo         string
	TicketKind   string
	TicketNumber int
	Branch       string
	BaseBranch   string
	AgentMention string
	AgentType    string
	SessionID    string
	ParentRunID  string
	StartedAt    time.Time
	FinishedAt   time.Time
	Error        string
	CreatedBy    string
}

type CreateRunInput struct {
	ID           string
	Kind         RunKind
	Status       Status
	Owner        string
	Repo         string
	TicketKind   string
	TicketNumber int
	Branch       string
	BaseBranch   string
	AgentMention string
	AgentType    string
	SessionID    string
	ParentRunID  string
	StartedAt    time.Time
	CreatedBy    string
}

type EventInput struct {
	RunID    string
	Time     time.Time
	Type     string
	Message  string
	DataJSON string
}

type LogChunkInput struct {
	RunID  string
	Time   time.Time
	Stream string
	Chunk  string
}

type LinkInput struct {
	RunID string
	Type  string
	URL   string
	Label string
}

type AuditEventInput struct {
	Time       time.Time
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	DataJSON   string
}

type ListAuditEventsOptions struct {
	Limit int
}

type Event struct {
	ID       int64
	RunID    string
	Time     time.Time
	Type     string
	Message  string
	DataJSON string
}

type LogChunk struct {
	ID     int64
	RunID  string
	Time   time.Time
	Stream string
	Chunk  string
}

type Link struct {
	ID    int64
	RunID string
	Type  string
	URL   string
	Label string
}

type AuditEvent struct {
	ID         int64
	Time       time.Time
	Actor      string
	Action     string
	TargetType string
	TargetID   string
	DataJSON   string
}

func validateStatusTransition(from, to Status) error {
	if from == to {
		return nil
	}
	switch from {
	case StatusQueued:
		if to == StatusRunning || to == StatusFailed || to == StatusCanceled || to == StatusTimeout {
			return nil
		}
	case StatusRunning:
		if to == StatusSuccess || to == StatusFailed || to == StatusCanceled || to == StatusTimeout {
			return nil
		}
	}
	return fmt.Errorf("%w: %s to %s", ErrInvalidStatusTransition, from, to)
}
