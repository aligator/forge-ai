package service

import (
	"context"
	"log/slog"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

type Forgejo interface {
	GetLatestPullReviewComments(context.Context, string, string, int) ([]forgejo.Comment, error)
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
	runtime *appruntime.Runtime
	handler *webhookHandler
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
	return &Service{
		runtime: rt,
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
	return s.runtime.CancelRun(id)
}
