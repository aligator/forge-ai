package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/gitops"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

const (
	WorkspaceModeSameBranchFreshWorkspace = "same_branch_fresh_workspace"
	WorkspaceModeExistingWorkspace        = "existing_workspace"
	WorkspaceModeManualContextOnly        = "manual_context_only"
)

type manualResumeInput struct {
	ParentRunID   string
	AgentMention  string
	SessionID     string
	WorkspaceMode string
	Prompt        string
	CreatedBy     string
}

// ManualResume creates and asynchronously runs a manual_resume run against the
// given parent run. Returns the new run's store ID so the caller can redirect
// to its detail page.
func (s *Service) ManualResume(ctx context.Context, parentRunID, agentMention, sessionID, workspaceMode, prompt, createdBy string) (string, error) {
	if strings.TrimSpace(prompt) == "" {
		return "", errors.New("prompt is required")
	}
	if s.handler.runner.runStore == nil {
		return "", errors.New("run store not configured")
	}
	parentRun, err := s.handler.runner.runStore.GetRun(ctx, parentRunID)
	if err != nil {
		return "", fmt.Errorf("get parent run: %w", err)
	}
	if agentMention == "" {
		agentMention = parentRun.AgentMention
	}
	if sessionID == "" {
		sessionID = parentRun.SessionID
	}
	if workspaceMode == "" {
		workspaceMode = WorkspaceModeSameBranchFreshWorkspace
	}
	if !validWorkspaceMode(workspaceMode) {
		return "", fmt.Errorf("invalid workspace mode %q", workspaceMode)
	}
	in := manualResumeInput{
		ParentRunID:   parentRunID,
		AgentMention:  agentMention,
		SessionID:     sessionID,
		WorkspaceMode: workspaceMode,
		Prompt:        prompt,
		CreatedBy:     createdBy,
	}
	ag, identity := s.handler.agentFor(agentMention)
	if ag == nil {
		return "", fmt.Errorf("agent %q not configured", agentMention)
	}
	run, err := s.handler.runner.createResumeRun(ctx, parentRun, in)
	if err != nil {
		return "", fmt.Errorf("create resume run: %w", err)
	}
	if run.ID == "" {
		return "", errors.New("run store did not return a run ID")
	}

	runCtx, runCancel := context.WithCancel(context.Background())
	s.registerCancel(run.ID, runCancel)

	spec := appruntime.RunSpec{
		Owner:  parentRun.Owner,
		Repo:   parentRun.Repo,
		Kind:   parentRun.TicketKind,
		Number: parentRun.TicketNumber,
	}
	if workspaceMode == WorkspaceModeSameBranchFreshWorkspace || workspaceMode == WorkspaceModeExistingWorkspace {
		spec.Branch = parentRun.Branch
	}

	go func() {
		defer func() {
			s.unregisterCancel(run.ID)
			runCancel()
		}()
		if err := s.runtime.SubmitWebhookRun(runCtx, spec, func(rCtx context.Context) error {
			return s.handler.runner.resume(rCtx, ag, parentRun, run, in, identity)
		}); err != nil {
			s.handler.logger.Error("manual resume failed", "run_id", run.ID, "error", err)
		}
	}()

	return run.ID, nil
}

func (r *workflowRunner) resume(ctx context.Context, ag Agent, parentRun, run runstore.Run, in manualResumeInput, identity config.GitIdentity) error {
	r.markRunRunning(ctx, run.ID)
	r.addRunEvent(ctx, run.ID, "resume_start", "manual resume started, mode="+in.WorkspaceMode)

	sessionID := in.SessionID
	if sessionID == "" {
		sessionID = parentRun.SessionID
	}

	workdir, cleanup, err := r.prepareResumeWorkspace(ctx, parentRun, in, identity)
	if err != nil {
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		return err
	}
	defer cleanup()

	r.addRunEvent(ctx, run.ID, "workspace_ready", "workspace ready, mode="+in.WorkspaceMode)

	_, agentErr := r.runAgent(ctx, ag, workdir, in.Prompt, sessionID, run.ID)
	if agentErr != nil {
		if ctx.Err() != nil {
			r.finishRun(context.Background(), run.ID, runstore.StatusCanceled, agentErr)
			return agentErr
		}
		agentErr = fmt.Errorf("agent failed: %w", agentErr)
		r.finishRun(ctx, run.ID, runstore.StatusFailed, agentErr)
		return agentErr
	}

	r.addRunEvent(ctx, run.ID, "agent_finished", "agent completed")
	r.finishRun(ctx, run.ID, runstore.StatusSuccess, nil)
	return nil
}

func (r *workflowRunner) prepareResumeWorkspace(ctx context.Context, parentRun runstore.Run, in manualResumeInput, identity config.GitIdentity) (string, func(), error) {
	noop := func() {}
	if in.WorkspaceMode == WorkspaceModeSameBranchFreshWorkspace {
		token := r.tokenForMention(in.AgentMention)
		cloneURL := strings.TrimRight(r.cfg.ForgejoURL, "/") + "/" + parentRun.Owner + "/" + parentRun.Repo + ".git"
		cloneURL = rewriteCloneURL(cloneURL, r.cfg.CloneURLBase)
		workdir, err := r.git.Prepare(ctx, r.cfg.WorkspaceDir, cloneURL, token, parentRun.Owner, parentRun.Repo, parentRun.Branch, parentRun.BaseBranch, identity)
		if err != nil {
			return "", noop, fmt.Errorf("prepare workspace: %w", err)
		}
		return workdir, func() { r.removeWorkspace(workdir) }, nil
	}
	if in.WorkspaceMode == WorkspaceModeExistingWorkspace {
		workdir := resumeWorkspacePath(r.cfg.WorkspaceDir, parentRun)
		if _, err := os.Stat(filepath.Join(workdir, ".git")); err != nil {
			if os.IsNotExist(err) {
				return "", noop, fmt.Errorf("existing workspace not available: %s", workdir)
			}
			return "", noop, fmt.Errorf("inspect existing workspace: %w", err)
		}
		return workdir, noop, nil
	}
	// manual_context_only: agent uses session context only, no git workspace needed
	workdir, err := os.MkdirTemp("", "forge-ai-resume-*")
	if err != nil {
		return "", noop, fmt.Errorf("create temp workspace: %w", err)
	}
	return workdir, func() { _ = os.RemoveAll(workdir) }, nil
}

func (r *workflowRunner) tokenForMention(mention string) string {
	for _, route := range r.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) && route.Token != "" {
			return route.Token
		}
	}
	return r.cfg.ForgejoToken
}

func (r *workflowRunner) createResumeRun(ctx context.Context, parentRun runstore.Run, in manualResumeInput) (runstore.Run, error) {
	if r.runStore == nil {
		return runstore.Run{}, errors.New("run store not configured")
	}
	agentType := ""
	for _, route := range r.cfg.Agents {
		if route.Disabled {
			continue
		}
		if strings.EqualFold(route.Mention, in.AgentMention) {
			agentType = route.Agent.Type
			break
		}
	}
	run, err := r.runStore.CreateRun(ctx, runstore.CreateRunInput{
		Kind:         runstore.RunKindManualResume,
		Status:       runstore.StatusQueued,
		Owner:        parentRun.Owner,
		Repo:         parentRun.Repo,
		TicketKind:   parentRun.TicketKind,
		TicketNumber: parentRun.TicketNumber,
		Branch:       parentRun.Branch,
		BaseBranch:   parentRun.BaseBranch,
		AgentMention: in.AgentMention,
		AgentType:    agentType,
		SessionID:    in.SessionID,
		ParentRunID:  in.ParentRunID,
		StartedAt:    timeNow(),
		CreatedBy:    in.CreatedBy,
	})
	if err != nil {
		return runstore.Run{}, err
	}
	r.addRunEvent(ctx, run.ID, "queued", "manual resume queued")
	return run, nil
}

func validWorkspaceMode(mode string) bool {
	switch mode {
	case WorkspaceModeSameBranchFreshWorkspace, WorkspaceModeExistingWorkspace, WorkspaceModeManualContextOnly:
		return true
	default:
		return false
	}
}

func resumeWorkspacePath(workspaceRoot string, parentRun runstore.Run) string {
	branch := gitops.BranchRefName(parentRun.Branch)
	return filepath.Join(workspaceRoot, gitops.Slug(parentRun.Owner+"-"+parentRun.Repo+"-"+branch))
}

func (h *webhookHandler) agentFor(mention string) (Agent, config.GitIdentity) {
	key := strings.ToLower(mention)
	ag := h.agents[key]
	for _, route := range h.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			return ag, route.Git
		}
	}
	return ag, h.cfg.Git.GitIdentity
}
