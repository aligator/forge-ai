package service

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

type Forgejo interface {
	GetPullReviewComments(context.Context, string, string, int, int64) ([]forgejo.Comment, error)
	CreateIssueComment(context.Context, string, string, int, string) error
	CreateCommentReaction(context.Context, string, string, int64, string) error
	FindOpenPullRequest(context.Context, string, string, string) (*forgejo.PullRequest, error)
	CreatePullRequest(context.Context, string, string, forgejo.CreatePullRequestRequest) (*forgejo.PullRequest, error)
	UpdatePullRequest(context.Context, string, string, int, forgejo.UpdatePullRequestRequest) (*forgejo.PullRequest, error)
}

type Git interface {
	Prepare(context.Context, string, string, string, string, string, string, string, config.GitIdentity) (string, error)
	CommitIfDirty(context.Context, string, string) (bool, error)
	Push(context.Context, string, string) error
}

type Agent interface {
	Run(context.Context, string, string, string) (agent.Result, error)
}

type optionsAgent interface {
	RunWithOptions(context.Context, agent.RunOptions) (agent.Result, error)
}

type agentSettingsGetter interface {
	GetAgentSettings(context.Context, string) (runstore.AgentSettings, error)
}

type Options struct {
	Config         config.Config
	Forgejo        Forgejo
	ForgejoClients map[string]Forgejo // mention (lowercase) → per-route client; falls back to Forgejo
	Git            Git
	Agents         map[string]Agent // mention (lowercase) → runner
	Runtime        *appruntime.Runtime
	RunStore       runstore.RunStore
	Logger         *slog.Logger
}

type Service struct {
	runtime  *appruntime.Runtime
	handler  *webhookHandler
	cancelMu sync.Mutex
	cancels  map[string]context.CancelFunc // store run ID → cancel func for manual resume runs
}

func New(options Options) *Service {
	rt := options.Runtime
	if rt == nil {
		rt = appruntime.New(options.Config.MaxConcurrent)
	}
	runner := &workflowRunner{
		cfg:            options.Config,
		forgejoClients: options.ForgejoClients,
		git:            options.Git,
		runStore:       options.RunStore,
		logger:         options.Logger,
	}
	svc := &Service{
		runtime: rt,
		cancels: make(map[string]context.CancelFunc),
		handler: &webhookHandler{
			cfg:            options.Config,
			forgejo:        options.Forgejo,
			forgejoClients: options.ForgejoClients,
			agents:         options.Agents,
			runtime:        rt,
			runner:         runner,
			logger:         options.Logger,
		},
	}
	runner.service = svc
	return svc
}

func (s *Service) Handle(ctx context.Context, event string, payload forgejo.WebhookPayload) error {
	return s.handler.Handle(ctx, event, payload)
}

func (s *Service) RuntimeSnapshot() appruntime.Snapshot {
	return s.runtime.Snapshot()
}

func (s *Service) Pause() {
	s.runtime.Pause()
}

func (s *Service) Resume() {
	s.runtime.Resume()
}

func (s *Service) CancelRun(id string) bool {
	return s.cancelRun(id)
}

func (s *Service) cancelRun(id string) bool {
	s.cancelMu.Lock()
	cancel, ok := s.cancels[id]
	s.cancelMu.Unlock()
	if ok {
		cancel()
		return true
	}
	return s.runtime.CancelRun(id)
}

func (s *Service) registerCancel(id string, cancel context.CancelFunc) {
	if id == "" || cancel == nil {
		return
	}
	s.cancelMu.Lock()
	s.cancels[id] = cancel
	s.cancelMu.Unlock()
}

func (s *Service) unregisterCancel(id string) {
	if id == "" {
		return
	}
	s.cancelMu.Lock()
	delete(s.cancels, id)
	s.cancelMu.Unlock()
}

func (s *Service) CancelRunAs(ctx context.Context, id, actor string) bool {
	ok := s.cancelRun(id)
	if ok {
		s.audit(ctx, actor, "run.cancel", "run", id, "")
		return true
	}
	if s.cancelStaleRun(ctx, id) {
		s.audit(ctx, actor, "run.cancel", "run", id, `{"mode":"stale"}`)
		return true
	}
	return false
}

func (s *Service) cancelStaleRun(ctx context.Context, id string) bool {
	if s.handler == nil || s.handler.runner == nil || s.handler.runner.runStore == nil {
		return false
	}
	store := s.handler.runner.runStore
	run, err := store.GetRun(ctx, id)
	if err != nil {
		if s.handler.logger != nil {
			s.handler.logger.Warn("get stale run for cancel failed", "run_id", id, "error", err)
		}
		return false
	}
	switch run.Status {
	case runstore.StatusQueued, runstore.StatusRunning:
	default:
		return false
	}
	message := "manually canceled stale run; no live runtime cancel handle"
	if err := store.UpdateRunStatus(ctx, id, runstore.StatusCanceled, timeNow(), message); err != nil {
		if s.handler.logger != nil {
			s.handler.logger.Warn("mark stale run canceled failed", "run_id", id, "error", err)
		}
		return false
	}
	if err := store.AddEvent(ctx, runstore.EventInput{
		Time:    timeNow(),
		RunID:   id,
		Type:    "canceled",
		Message: message,
	}); err != nil && s.handler.logger != nil {
		s.handler.logger.Warn("record stale cancel event failed", "run_id", id, "error", err)
	}
	return true
}

func (s *Service) PauseQueue(ctx context.Context, actor string) {
	s.runtime.Pause()
	s.audit(ctx, actor, "queue.pause", "queue", "default", "")
}

func (s *Service) ResumeQueue(ctx context.Context, actor string) {
	s.runtime.Resume()
	s.audit(ctx, actor, "queue.resume", "queue", "default", "")
}

func (s *Service) SetMaxConcurrent(ctx context.Context, maxConcurrent int, actor string) error {
	if err := s.runtime.SetMaxConcurrent(maxConcurrent); err != nil {
		return err
	}
	s.audit(ctx, actor, "queue.set_max_concurrent", "queue", "default", fmt.Sprintf(`{"max_concurrent":%d}`, maxConcurrent))
	return nil
}

func (s *Service) audit(ctx context.Context, actor, action, targetType, targetID, dataJSON string) {
	if s.handler == nil || s.handler.runner == nil || s.handler.runner.runStore == nil {
		return
	}
	if actor == "" {
		actor = "internal"
	}
	if err := s.handler.runner.runStore.AddAuditEvent(ctx, runstore.AuditEventInput{
		Time:       time.Now().UTC(),
		Actor:      actor,
		Action:     action,
		TargetType: targetType,
		TargetID:   targetID,
		DataJSON:   dataJSON,
	}); err != nil && s.handler.logger != nil {
		s.handler.logger.Warn("record audit event failed", "action", action, "target_type", targetType, "target_id", targetID, "error", err)
	}
}
