package service

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

func (s *Service) RetryRun(ctx context.Context, parentRunID, actor string) (string, error) {
	if s.handler.runner.runStore == nil {
		return "", errors.New("run store not configured")
	}
	parentRun, err := s.handler.runner.runStore.GetRun(ctx, parentRunID)
	if err != nil {
		return "", fmt.Errorf("get parent run: %w", err)
	}
	if parentRun.Status != runstore.StatusFailed && parentRun.Status != runstore.StatusCanceled {
		return "", fmt.Errorf("run status %q cannot be retried", parentRun.Status)
	}
	ag, identity := s.handler.agentFor(parentRun.AgentMention)
	if ag == nil {
		return "", fmt.Errorf("agent %q not configured", parentRun.AgentMention)
	}
	run, err := s.handler.runner.createRetryRun(ctx, parentRun, actor)
	if err != nil {
		return "", fmt.Errorf("create retry run: %w", err)
	}
	s.audit(ctx, actor, "run.retry", "run", parentRunID, `{"new_run_id":"`+run.ID+`"}`)

	ticket := s.handler.runner.ticketForRun(parentRun)
	fc := s.handler.forgejoFor(parentRun.AgentMention)
	runCtx, runCancel := context.WithCancel(context.Background())
	s.registerCancel(run.ID, runCancel)

	go func() {
		defer func() {
			s.unregisterCancel(run.ID)
			runCancel()
		}()
		spec := appruntime.RunSpec{
			TicketRef: ticket.Ref(),
			Branch:    parentRun.Branch,
			Owner:     parentRun.Owner,
			Repo:      parentRun.Repo,
			Kind:      parentRun.TicketKind,
			Number:    parentRun.TicketNumber,
		}
		err := s.runtime.SubmitWebhookRun(runCtx, spec, func(rCtx context.Context) error {
			return s.handler.runner.run(rCtx, fc, ticket, ag, run, identity)
		})
		switch {
		case errors.Is(err, context.Canceled):
			s.handler.runner.finishRun(context.Background(), run.ID, runstore.StatusCanceled, err)
		case errors.Is(err, appruntime.ErrTicketActive), errors.Is(err, appruntime.ErrBranchActive):
			s.handler.runner.finishRun(context.Background(), run.ID, runstore.StatusFailed, err)
		case err != nil:
			s.handler.logger.Error("retry run failed", "run_id", run.ID, "error", err)
		}
	}()

	return run.ID, nil
}

func (r *workflowRunner) createRetryRun(ctx context.Context, parentRun runstore.Run, actor string) (runstore.Run, error) {
	run, err := r.runStore.CreateRun(ctx, runstore.CreateRunInput{
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusQueued,
		Owner:        parentRun.Owner,
		Repo:         parentRun.Repo,
		TicketKind:   parentRun.TicketKind,
		TicketNumber: parentRun.TicketNumber,
		Branch:       parentRun.Branch,
		BaseBranch:   parentRun.BaseBranch,
		AgentMention: parentRun.AgentMention,
		AgentType:    parentRun.AgentType,
		ParentRunID:  parentRun.ID,
		StartedAt:    timeNow(),
		CreatedBy:    actor,
	})
	if err != nil {
		return runstore.Run{}, err
	}
	r.addRunEvent(ctx, run.ID, "queued", "retry queued from "+parentRun.ID)
	return run, nil
}

func (r *workflowRunner) ticketForRun(run runstore.Run) forgejo.Ticket {
	base := strings.TrimRight(r.cfg.ForgejoURL, "/")
	ticketURL := ""
	cloneURL := ""
	if base != "" && run.Owner != "" && run.Repo != "" {
		pathKind := "issues"
		if run.TicketKind == "pr" {
			pathKind = "pulls"
		}
		ticketURL = base + "/" + run.Owner + "/" + run.Repo + "/" + pathKind + "/" + fmt.Sprint(run.TicketNumber)
		cloneURL = base + "/" + run.Owner + "/" + run.Repo + ".git"
	}
	return forgejo.Ticket{
		Owner:         run.Owner,
		Repo:          run.Repo,
		CloneURL:      rewriteCloneURL(cloneURL, r.cfg.CloneURLBase),
		DefaultBranch: firstNonEmpty(run.BaseBranch, "main"),
		Kind:          run.TicketKind,
		Number:        run.TicketNumber,
		Title:         fmt.Sprintf("Retry %s #%d", run.TicketKind, run.TicketNumber),
		HTMLURL:       ticketURL,
		BaseBranch:    run.BaseBranch,
		Instruction:   strings.TrimSpace(run.AgentMention + " retry previous failed or canceled run"),
	}
}
