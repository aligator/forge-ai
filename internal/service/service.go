package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/gitops"
)

type Forgejo interface {
	GetLatestPullReviewComments(context.Context, string, string, int) ([]forgejo.Comment, error)
	CreateIssueComment(context.Context, string, string, int, string) error
	CreateCommentReaction(context.Context, string, string, int64, string) error
	FindOpenPullRequest(context.Context, string, string, string) (*forgejo.PullRequest, error)
	CreatePullRequest(context.Context, string, string, forgejo.CreatePullRequestRequest) (*forgejo.PullRequest, error)
}

type Git interface {
	Prepare(context.Context, string, string, string, string, string, string, string) (string, error)
	CommitIfDirty(context.Context, string, string) (bool, error)
	Push(context.Context, string, string) error
}

type Agent interface {
	Run(context.Context, string, string) (agent.Result, error)
}

type Options struct {
	Config  config.Config
	Forgejo Forgejo
	Git     Git
	Agent   Agent
	Logger  *slog.Logger
}

type Service struct {
	cfg       config.Config
	forgejo   Forgejo
	git       Git
	agent     Agent
	logger    *slog.Logger
	semaphore chan struct{}
}

func New(options Options) *Service {
	return &Service{
		cfg:       options.Config,
		forgejo:   options.Forgejo,
		git:       options.Git,
		agent:     options.Agent,
		logger:    options.Logger,
		semaphore: make(chan struct{}, options.Config.MaxConcurrent),
	}
}

func (s *Service) Handle(ctx context.Context, event string, payload forgejo.WebhookPayload) error {
	ticket, ok := forgejo.TicketFromPayload(event, payload)
	if !ok {
		s.logger.Info("ignored webhook without supported ticket", "event", event, "action", payload.Action)
		return nil
	}

	s.logger.Debug("ticket from payload",
		"ticket", ticket.Ref(),
		"title", ticket.Title,
		"instruction", ticket.Instruction,
		"comment_id", ticket.CommentID,
		"has_review", payload.Review != nil,
	)

	if ticket.Instruction == "" && event == "pull_request_comment" && payload.Action == "reviewed" {
		comments, err := s.forgejo.GetLatestPullReviewComments(ctx, ticket.Owner, ticket.Repo, ticket.Number)
		if err != nil {
			s.logger.Warn("fetch review comments failed", "error", err)
		} else {
			s.logger.Debug("fetched review comments", "count", len(comments))
			for _, c := range comments {
				if strings.Contains(strings.ToLower(c.Body), strings.ToLower(s.cfg.TriggerMention)) && ticket.CommentID == 0 {
					ticket.CommentID = c.ID
				}
				if ticket.Instruction == "" {
					ticket.Instruction = c.Body
				} else {
					ticket.Instruction += "\n" + c.Body
				}
			}
		}
	}

	if !s.shouldRun(payload, ticket) {
		s.logger.Info("ignored webhook without mention",
			"event", event,
			"action", payload.Action,
			"ticket", ticket.Ref(),
			"mention", s.cfg.TriggerMention,
			"instruction_len", len(strings.TrimSpace(ticket.Instruction)),
		)
		return nil
	}

	if err := s.postStartAck(ctx, ticket); err != nil {
		s.logger.Warn("post start acknowledgement failed", "comment_id", ticket.CommentID, "error", err)
	}

	s.semaphore <- struct{}{}
	defer func() { <-s.semaphore }()

	return s.run(ctx, ticket)
}

func (s *Service) shouldRun(payload forgejo.WebhookPayload, ticket forgejo.Ticket) bool {
	if payload.Sender != nil && payload.Sender.Handle() == s.cfg.ForgejoBootstrapUser {
		return false
	}
	return strings.Contains(strings.ToLower(ticket.Instruction), strings.ToLower(s.cfg.TriggerMention))
}

func (s *Service) run(ctx context.Context, ticket forgejo.Ticket) error {
	branch := branchForTicket(s.cfg, ticket)
	base := firstNonEmpty(ticket.BaseBranch, ticket.DefaultBranch, "main")

	s.logger.Info("starting ticket workflow", "ticket", ticket.Ref(), "repo", ticket.Owner+"/"+ticket.Repo, "branch", branch)
	if err := s.postStart(ctx, ticket, branch); err != nil {
		s.logger.Warn("post start comment failed", "error", err)
	}

	cloneURL := rewriteCloneURL(ticket.CloneURL, s.cfg.CloneURLBase)
	workdir, err := s.git.Prepare(ctx, s.cfg.WorkspaceDir, cloneURL, s.cfg.ForgejoToken, ticket.Owner, ticket.Repo, branch, base)
	if err != nil {
		_ = s.postFailure(ctx, ticket, err)
		return err
	}

	s.logger.Info("workspace ready", "workdir", workdir, "branch", branch)
	result, agentErr := s.agent.Run(ctx, workdir, prompt(ticket, branch, base, s.cfg.AgentAllowGit))
	if agentErr != nil {
		err := fmt.Errorf("agent failed: %w", agentErr)
		_ = s.postFailureWithOutput(ctx, ticket, err, result.Output)
		return err
	}

	commitMsg := readAndRemoveCommitMsg(workdir)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("forge-ai: work on %s #%d", ticket.Kind, ticket.Number)
	}
	committed, err := s.git.CommitIfDirty(ctx, workdir, commitMsg)
	if err != nil {
		_ = s.postFailureWithOutput(ctx, ticket, err, result.Output)
		return err
	}

	if err := s.git.Push(ctx, workdir, branch); err != nil {
		_ = s.postFailureWithOutput(ctx, ticket, err, result.Output)
		return err
	}

	prText := ""
	if s.cfg.CreatePR && ticket.Kind == "issue" {
		pull, err := s.ensurePullRequest(ctx, ticket, branch, base)
		if err != nil {
			_ = s.postFailureWithOutput(ctx, ticket, err, result.Output)
			return err
		}
		if pull != nil {
			prText = fmt.Sprintf("\n\nPull request: %s", firstNonEmpty(pull.HTMLURL, fmt.Sprintf("#%d", pull.NumberValue())))
		}
	}

	comment := successComment(branch, committed, result.Output, prText)
	if err := s.postSuccess(ctx, ticket, comment); err != nil {
		return err
	}

	s.logger.Info("ticket workflow completed", "ticket", ticket.Ref(), "branch", branch, "committed", committed)
	return nil
}

func (s *Service) ensurePullRequest(ctx context.Context, ticket forgejo.Ticket, branch, base string) (*forgejo.PullRequest, error) {
	existing, err := s.forgejo.FindOpenPullRequest(ctx, ticket.Owner, ticket.Repo, branch)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	return s.forgejo.CreatePullRequest(ctx, ticket.Owner, ticket.Repo, forgejo.CreatePullRequestRequest{
		Base:  base,
		Head:  branch,
		Title: "forge-ai: " + ticket.Title,
		Body:  fmt.Sprintf("Automated work for %s #%d.\n\n%s", ticket.Kind, ticket.Number, ticket.HTMLURL),
	})
}

func (s *Service) postFailure(ctx context.Context, ticket forgejo.Ticket, err error) error {
	return s.postFailureWithOutput(ctx, ticket, err, "")
}

func (s *Service) postFailureWithOutput(ctx context.Context, ticket forgejo.Ticket, err error, output string) error {
	body := "forge-ai failed: `" + sanitizeInline(err.Error()) + "`"
	if strings.TrimSpace(output) != "" {
		body += "\n\nLast agent output:\n\n```text\n" + fence(output) + "\n```"
	}
	return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func (s *Service) postStartAck(ctx context.Context, ticket forgejo.Ticket) error {
	if ticket.CommentID != 0 {
		if err := s.forgejo.CreateCommentReaction(ctx, ticket.Owner, ticket.Repo, ticket.CommentID, "eyes"); err == nil {
			return nil
		}
		return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, ":eyes:")
	}
	if strings.TrimSpace(ticket.Instruction) != "" {
		return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, ":eyes:")
	}
	return nil
}

func (s *Service) postStart(ctx context.Context, ticket forgejo.Ticket, branch string) error {
	if ticket.CommentID != 0 || strings.TrimSpace(ticket.Instruction) != "" {
		return nil
	}
	return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, "forge-ai: starting work on `"+branch+"`.")
}

func (s *Service) postSuccess(ctx context.Context, ticket forgejo.Ticket, body string) error {
	if ticket.CommentID != 0 {
		if err := s.forgejo.CreateCommentReaction(ctx, ticket.Owner, ticket.Repo, ticket.CommentID, "+1"); err == nil {
			return nil
		}
		return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
	}
	return s.forgejo.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func prompt(ticket forgejo.Ticket, branch, base string, allowGit bool) string {
	gitPolicy := `The repository is already checked out on the prepared branch above. Do not run git commands. Make file changes only; the outer forge-ai service will commit and push the prepared branch.`
	if allowGit {
		gitPolicy = `The repository is already checked out on the prepared branch above. Stay on that branch. You may use git status, git diff, git add, and git commit on the current branch only. Do not create, switch, reset, rebase, merge, or delete branches. Do not push; the outer forge-ai service will push the prepared branch and post back to Forgejo.`
	}
	return fmt.Sprintf(`You are working in a cloned Forgejo repository.

Repository: %s/%s
Ticket: %s #%d
Branch: %s
Base branch: %s
Ticket URL: %s

Title:
%s

Body:
%s

Trigger comment:
%s

Treat the trigger comment as the primary instruction. Use the issue or pull request body only as background context.
%s

Before making any changes, explore the repository to understand its structure and existing code. If an AGENTS.md or CLAUDE.md read and follow its instructions. Then implement the requested change. If you cannot complete the requested change, explain the blocker in your final response. If all is good you may write a short sumary as final response.

When done, write a short conventional-commits commit message to the file ".forge-ai-commit-msg" in the repository root. One line only. Do not commit or push.`,
		ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number, branch, base, ticket.HTMLURL, ticket.Title, ticket.Body, strings.TrimSpace(ticket.Instruction), gitPolicy)
}

func successComment(branch string, committed bool, output, prText string) string {
	status := "forge-ai completed work on `" + branch + "`."
	if committed {
		status += "\n\nCommitted remaining changes."
	} else {
		status += "\n\nNo uncommitted changes remained after the agent finished."
	}
	if strings.TrimSpace(output) != "" {
		status += "\n\nLast agent output:\n\n```text\n" + fence(output) + "\n```"
	}
	return status + prText
}

func sanitizeInline(value string) string {
	return strings.ReplaceAll(value, "`", "'")
}

func fence(value string) string {
	return strings.ReplaceAll(strings.TrimSpace(value), "```", "'''")
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

func branchForTicket(cfg config.Config, ticket forgejo.Ticket) string {
	if ticket.Kind == "pr" && ticket.HeadBranch != "" {
		return ticket.HeadBranch
	}
	return gitops.BranchName(cfg.BranchPrefix, ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number)
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
