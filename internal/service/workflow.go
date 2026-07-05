package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/gitops"
	"codeberg.org/forge-ai/internal/runstore"
)

type workflowRunner struct {
	cfg            config.Config
	forgejoClients map[string]Forgejo
	git            Git
	runStore       runstore.RunStore
	logger         *slog.Logger
}

func (r *workflowRunner) run(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, ag Agent, run runstore.Run, identity config.GitIdentity) error {
	branch := firstNonEmpty(run.Branch, branchForTicket(r.cfg, ticket))
	base := branchRef(firstNonEmpty(run.BaseBranch, ticket.BaseBranch, ticket.DefaultBranch, "main"))

	r.logger.Info("starting ticket workflow", "ticket", ticket.Ref(), "repo", ticket.Owner+"/"+ticket.Repo, "branch", branch)
	r.markRunRunning(ctx, run.ID)
	if err := postStart(ctx, fc, ticket, branch); err != nil {
		r.logger.Warn("post start comment failed", "error", err)
	}

	token := r.routeToken(ticket, fc)
	cloneURL := rewriteCloneURL(ticket.CloneURL, r.cfg.CloneURLBase)
	workdir, err := r.git.Prepare(ctx, r.cfg.WorkspaceDir, cloneURL, token, ticket.Owner, ticket.Repo, branch, base, identity)
	if err != nil {
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = postFailure(ctx, fc, ticket, err)
		return err
	}
	r.addRunEvent(ctx, run.ID, "workspace_ready", "workspace prepared")

	r.logger.Info("workspace ready", "workdir", workdir, "branch", branch)
	r.logWorkspaceFiles(workdir, "before agent run")
	sessionID := sessionIDFromInstruction(ticket.Instruction, r.cfg.Agents)
	result, agentErr := ag.Run(ctx, workdir, prompt(ticket, branch, base, r.cfg.AgentAllowGit, r.cfg.AgentToolHints), sessionID)
	r.recordAgentResult(ctx, run.ID, result)
	r.logWorkspaceFiles(workdir, "after agent run")
	if agentErr != nil {
		err := fmt.Errorf("agent failed: %w", agentErr)
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	r.addRunEvent(ctx, run.ID, "agent_finished", "agent completed")

	commitMsg := readAndRemoveCommitMsg(workdir)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("forge-ai: work on %s #%d", ticket.Kind, ticket.Number)
	}
	committed, err := r.git.CommitIfDirty(ctx, workdir, commitMsg)
	if err != nil {
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	r.addRunEvent(ctx, run.ID, "commit_checked", fmt.Sprintf("committed=%t", committed))

	if err := r.git.Push(ctx, workdir, branch); err != nil {
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	r.addRunEvent(ctx, run.ID, "pushed", "branch pushed")
	defer r.removeWorkspace(workdir)

	prText := ""
	if r.cfg.CreatePR && ticket.Kind == "issue" {
		pull, err := ensurePullRequest(ctx, fc, ticket, branch, base)
		if err != nil {
			r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
			_ = postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
			return err
		}
		if pull != nil {
			prText = fmt.Sprintf("\n\nPull request: %s", firstNonEmpty(pull.HTMLURL, fmt.Sprintf("#%d", pull.NumberValue())))
			r.addRunLink(ctx, run.ID, "pull_request", pull.HTMLURL, fmt.Sprintf("PR #%d", pull.NumberValue()))
		}
	}

	comment := successComment(branch, committed, result.SessionID, prText)
	if err := postSuccess(ctx, fc, ticket, comment); err != nil {
		r.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		return err
	}

	r.finishRun(ctx, run.ID, runstore.StatusSuccess, nil)
	r.logger.Info("ticket workflow completed", "ticket", ticket.Ref(), "branch", branch, "committed", committed)
	return nil
}

func (r *workflowRunner) createRun(ctx context.Context, payload forgejo.WebhookPayload, ticket forgejo.Ticket, mention, branch, base string) (runstore.Run, error) {
	if r.runStore == nil {
		return runstore.Run{Branch: branch, BaseBranch: base}, nil
	}
	agentType := ""
	for _, route := range r.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			agentType = route.Agent.Type
			break
		}
	}
	createdBy := ""
	if payload.Sender != nil {
		createdBy = payload.Sender.Handle()
	}
	run, err := r.runStore.CreateRun(ctx, runstore.CreateRunInput{
		Kind:         runstore.RunKindWebhookRun,
		Status:       runstore.StatusQueued,
		Owner:        ticket.Owner,
		Repo:         ticket.Repo,
		TicketKind:   ticket.Kind,
		TicketNumber: ticket.Number,
		Branch:       branch,
		BaseBranch:   base,
		AgentMention: mention,
		AgentType:    agentType,
		StartedAt:    timeNow(),
		CreatedBy:    createdBy,
	})
	if err != nil {
		return runstore.Run{}, err
	}
	r.addRunEvent(ctx, run.ID, "queued", "webhook accepted")
	r.addRunLink(ctx, run.ID, "ticket", ticket.HTMLURL, ticket.Ref())
	return run, nil
}

func (r *workflowRunner) markRunRunning(ctx context.Context, runID string) {
	if r.runStore == nil || runID == "" {
		return
	}
	if err := r.runStore.UpdateRunStatus(ctx, runID, runstore.StatusRunning, time.Time{}, ""); err != nil {
		r.logger.Warn("record run status failed", "run_id", runID, "status", runstore.StatusRunning, "error", err)
	}
	r.addRunEvent(ctx, runID, "running", "workflow started")
}

func (r *workflowRunner) finishRun(ctx context.Context, runID string, status runstore.Status, runErr error) {
	if r.runStore == nil || runID == "" {
		return
	}
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	if err := r.runStore.UpdateRunStatus(ctx, runID, status, timeNow(), message); err != nil {
		r.logger.Warn("record run status failed", "run_id", runID, "status", status, "error", err)
	}
	r.addRunEvent(ctx, runID, string(status), message)
}

func (r *workflowRunner) recordAgentResult(ctx context.Context, runID string, result agent.Result) {
	if r.runStore == nil || runID == "" {
		return
	}
	if strings.TrimSpace(result.SessionID) != "" {
		if err := r.runStore.SetSessionID(ctx, runID, result.SessionID); err != nil {
			r.logger.Warn("record run session failed", "run_id", runID, "error", err)
		}
	}
	if result.Output != "" {
		if err := r.runStore.AddLogChunk(ctx, runstore.LogChunkInput{
			RunID:  runID,
			Time:   timeNow(),
			Stream: "combined",
			Chunk:  result.Output,
		}); err != nil {
			r.logger.Warn("record run log failed", "run_id", runID, "error", err)
		}
	}
}

func (r *workflowRunner) addRunEvent(ctx context.Context, runID, eventType, message string) {
	if r.runStore == nil || runID == "" {
		return
	}
	if err := r.runStore.AddEvent(ctx, runstore.EventInput{
		RunID:   runID,
		Time:    timeNow(),
		Type:    eventType,
		Message: message,
	}); err != nil {
		r.logger.Warn("record run event failed", "run_id", runID, "type", eventType, "error", err)
	}
}

func (r *workflowRunner) addRunLink(ctx context.Context, runID, linkType, url, label string) {
	if r.runStore == nil || runID == "" || strings.TrimSpace(url) == "" {
		return
	}
	if err := r.runStore.AddLink(ctx, runstore.LinkInput{
		RunID: runID,
		Type:  linkType,
		URL:   url,
		Label: label,
	}); err != nil {
		r.logger.Warn("record run link failed", "run_id", runID, "type", linkType, "error", err)
	}
}

var timeNow = func() time.Time {
	return time.Now().UTC()
}

func ensurePullRequest(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, branch, base string) (*forgejo.PullRequest, error) {
	base = branchRef(base)
	existing, err := fc.FindOpenPullRequest(ctx, ticket.Owner, ticket.Repo, branch)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if existing.Base.Ref != base {
			return fc.UpdatePullRequest(ctx, ticket.Owner, ticket.Repo, existing.NumberValue(), forgejo.UpdatePullRequestRequest{
				Base: base,
			})
		}
		return existing, nil
	}

	created, err := fc.CreatePullRequest(ctx, ticket.Owner, ticket.Repo, forgejo.CreatePullRequestRequest{
		Base:  base,
		Head:  branch,
		Title: "forge-ai: " + ticket.Title,
		Body:  fmt.Sprintf("Automated work for %s #%d.\n\n%s", ticket.Kind, ticket.Number, ticket.HTMLURL),
	})
	if err == nil {
		return created, nil
	}

	existing, lookupErr := fc.FindOpenPullRequest(ctx, ticket.Owner, ticket.Repo, branch)
	if lookupErr == nil && existing != nil {
		return existing, nil
	}
	return nil, err
}

func postFailure(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, err error) error {
	return postFailureWithOutput(ctx, fc, ticket, err, "", "")
}

func postFailureWithOutput(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, err error, output, sessionID string) error {
	body := "forge-ai failed: `" + sanitizeInline(err.Error()) + "`"
	if strings.TrimSpace(sessionID) != "" {
		body += "\n\nAgent session: `" + sanitizeInline(sessionID) + "`"
	}
	if strings.TrimSpace(output) != "" {
		body += "\n\nLast agent output:\n\n```text\n" + fence(output) + "\n```"
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func postStartAckWith(ctx context.Context, fc Forgejo, ticket forgejo.Ticket) error {
	if ticket.CommentID != 0 {
		if err := fc.CreateCommentReaction(ctx, ticket.Owner, ticket.Repo, ticket.CommentID, "eyes"); err == nil {
			return nil
		}
		return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, ":eyes:")
	}
	if strings.TrimSpace(ticket.Instruction) != "" {
		return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, ":eyes:")
	}
	return nil
}

func postStart(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, branch string) error {
	if ticket.CommentID != 0 || strings.TrimSpace(ticket.Instruction) != "" {
		return nil
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, "forge-ai: starting work on `"+branch+"`.")
}

func postSuccess(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, body string) error {
	if ticket.CommentID != 0 {
		if strings.Contains(body, "Agent session:") {
			return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
		}
		if err := fc.CreateCommentReaction(ctx, ticket.Owner, ticket.Repo, ticket.CommentID, "+1"); err == nil {
			return nil
		}
		return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func successComment(branch string, committed bool, sessionID, prText string) string {
	status := "forge-ai completed work on `" + branch + "`."
	if committed {
		status += "\n\nCommitted remaining changes."
	} else {
		status += "\n\nNo uncommitted changes remained after the agent finished."
	}
	if strings.TrimSpace(sessionID) != "" {
		status += "\n\nAgent session: `" + sanitizeInline(sessionID) + "`"
	}
	return status + prText
}

func sanitizeInline(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func fence(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "```", "'''")
}

func (r *workflowRunner) removeWorkspace(workdir string) {
	if workdir == "" || filepath.Clean(workdir) == filepath.Clean(r.cfg.WorkspaceDir) {
		r.logger.Warn("skip unsafe workspace cleanup", "workdir", workdir)
		return
	}
	if err := os.RemoveAll(workdir); err != nil {
		r.logger.Warn("workspace cleanup failed", "workdir", workdir, "error", err)
		return
	}
	r.logger.Info("workspace removed", "workdir", workdir)
}

// routeToken returns the Forgejo token for the given Forgejo client by looking up which route it belongs to.
// Falls back to the global token.
func (r *workflowRunner) routeToken(ticket forgejo.Ticket, fc Forgejo) string {
	if r.forgejoClients != nil {
		for mention, client := range r.forgejoClients {
			if client == fc {
				for _, route := range r.cfg.Agents {
					if strings.ToLower(route.Mention) == mention && route.Token != "" {
						return route.Token
					}
				}
			}
		}
	}
	return r.cfg.ForgejoToken
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func readAndRemoveCommitMsg(workdir string) string {
	path := filepath.Join(workdir, ".forge-ai-commit-msg")
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_ = os.Remove(path)
	return strings.TrimSpace(string(data))
}

func (r *workflowRunner) logWorkspaceFiles(workdir, label string) {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		r.logger.Info("workspace listing failed", "label", label, "error", err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	r.logger.Info("workspace contents", "label", label, "workdir", workdir, "files", strings.Join(names, ", "))
}

func branchForTicket(cfg config.Config, ticket forgejo.Ticket) string {
	if ticket.Kind == "pr" && ticket.HeadBranch != "" {
		return branchRef(ticket.HeadBranch)
	}
	return gitops.BranchName(cfg.BranchPrefix, ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number)
}

func branchRef(value string) string {
	return gitops.BranchRefName(value)
}

func rewriteCloneURL(rawCloneURL, rawBase string) string {
	if rawCloneURL == "" || rawBase == "" {
		return rawCloneURL
	}
	cloneURL, err := url.Parse(rawCloneURL)
	if err != nil || cloneURL.Scheme == "" || cloneURL.Host == "" {
		return rawCloneURL
	}
	baseURL, err := url.Parse(rawBase)
	if err != nil || baseURL.Scheme == "" || baseURL.Host == "" {
		return rawCloneURL
	}
	cloneURL.Scheme = baseURL.Scheme
	cloneURL.Host = baseURL.Host
	return cloneURL.String()
}
