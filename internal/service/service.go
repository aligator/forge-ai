package service

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"

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
	Config         config.Config
	Forgejo        Forgejo
	ForgejoClients map[string]Forgejo // mention (lowercase) → per-route client; falls back to Forgejo
	Git            Git
	Agents         map[string]Agent // mention (lowercase) → runner
	Logger         *slog.Logger
}

type Service struct {
	cfg            config.Config
	forgejo        Forgejo
	forgejoClients map[string]Forgejo
	git            Git
	agents         map[string]Agent
	logger         *slog.Logger
	semaphore      chan struct{}
	mu             sync.Mutex
	activeTickets  map[string]struct{}
}

func New(options Options) *Service {
	return &Service{
		cfg:            options.Config,
		forgejo:        options.Forgejo,
		forgejoClients: options.ForgejoClients,
		git:            options.Git,
		agents:         options.Agents,
		logger:         options.Logger,
		semaphore:      make(chan struct{}, options.Config.MaxConcurrent),
		activeTickets:  make(map[string]struct{}),
	}
}

// forgejoFor returns the Forgejo client for the given mention, falling back to the global client.
func (s *Service) forgejoFor(mention string) Forgejo {
	if s.forgejoClients != nil {
		if fc, ok := s.forgejoClients[strings.ToLower(mention)]; ok {
			return fc
		}
	}
	return s.forgejo
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
				if s.anyMentionIn(c.Body) && ticket.CommentID == 0 {
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
		sender := ""
		if payload.Sender != nil {
			sender = payload.Sender.Handle()
		}
		var mentions []string
		for _, route := range s.cfg.Agents {
			mentions = append(mentions, route.Mention)
		}
		s.logger.Info("ignored webhook without mention",
			"event", event,
			"action", payload.Action,
			"ticket", ticket.Ref(),
			"sender", sender,
			"bootstrap_user", s.cfg.ForgejoBootstrapUser,
			"instruction", fmt.Sprintf("%q", ticket.Instruction),
			"configured_mentions", mentions,
		)
		return nil
	}

	ref := ticket.Ref()
	s.mu.Lock()
	if _, busy := s.activeTickets[ref]; busy {
		s.mu.Unlock()
		s.logger.Info("ignored webhook, ticket already active", "ticket", ref)
		return nil
	}
	s.activeTickets[ref] = struct{}{}
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.activeTickets, ref)
		s.mu.Unlock()
	}()

	mention, ag := s.findAgent(ticket.Instruction)
	fc := s.forgejoFor(mention)

	if err := s.postStartAckWith(ctx, fc, ticket); err != nil {
		s.logger.Warn("post start acknowledgement failed", "comment_id", ticket.CommentID, "error", err)
	}

	s.semaphore <- struct{}{}
	defer func() { <-s.semaphore }()

	return s.run(ctx, fc, ticket, ag)
}

func (s *Service) shouldRun(payload forgejo.WebhookPayload, ticket forgejo.Ticket) bool {
	lower := strings.ToLower(ticket.Instruction)
	for _, route := range s.cfg.Agents {
		if !strings.Contains(lower, strings.ToLower(route.Mention)) {
			continue
		}
		// Block if the sender is this route's own user (self-loop prevention)
		if payload.Sender != nil {
			handle := payload.Sender.Handle()
			agentUser := route.User
			if agentUser == "" {
				agentUser = s.cfg.ForgejoBootstrapUser
			}
			if handle == agentUser {
				return false
			}
		}
		return true
	}
	return false
}

func (s *Service) anyMentionIn(text string) bool {
	lower := strings.ToLower(text)
	for _, route := range s.cfg.Agents {
		if strings.Contains(lower, strings.ToLower(route.Mention)) {
			return true
		}
	}
	return false
}

// findAgent returns the matched mention and runner for the first mention found in instruction.
// Assumes shouldRun already confirmed a match exists.
func (s *Service) findAgent(instruction string) (string, Agent) {
	lower := strings.ToLower(instruction)
	for _, route := range s.cfg.Agents {
		if strings.Contains(lower, strings.ToLower(route.Mention)) {
			key := strings.ToLower(route.Mention)
			if ag, ok := s.agents[key]; ok {
				return key, ag
			}
		}
	}
	return "", nil
}

func (s *Service) run(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, ag Agent) error {
	branch := branchForTicket(s.cfg, ticket)
	base := firstNonEmpty(ticket.BaseBranch, ticket.DefaultBranch, "main")

	s.logger.Info("starting ticket workflow", "ticket", ticket.Ref(), "repo", ticket.Owner+"/"+ticket.Repo, "branch", branch)
	if err := s.postStart(ctx, fc, ticket, branch); err != nil {
		s.logger.Warn("post start comment failed", "error", err)
	}

	token := s.routeToken(ticket, fc)
	cloneURL := rewriteCloneURL(ticket.CloneURL, s.cfg.CloneURLBase)
	workdir, err := s.git.Prepare(ctx, s.cfg.WorkspaceDir, cloneURL, token, ticket.Owner, ticket.Repo, branch, base)
	if err != nil {
		_ = s.postFailure(ctx, fc, ticket, err)
		return err
	}

	s.logger.Info("workspace ready", "workdir", workdir, "branch", branch)
	s.logWorkspaceFiles(workdir, "before agent run")
	result, agentErr := ag.Run(ctx, workdir, prompt(ticket, branch, base, s.cfg.AgentAllowGit, s.cfg.AgentToolHints))
	s.logWorkspaceFiles(workdir, "after agent run")
	if agentErr != nil {
		err := fmt.Errorf("agent failed: %w", agentErr)
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output)
		return err
	}

	commitMsg := readAndRemoveCommitMsg(workdir)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("forge-ai: work on %s #%d", ticket.Kind, ticket.Number)
	}
	committed, err := s.git.CommitIfDirty(ctx, workdir, commitMsg)
	if err != nil {
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output)
		return err
	}

	if err := s.git.Push(ctx, workdir, branch); err != nil {
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output)
		return err
	}

	prText := ""
	if s.cfg.CreatePR && ticket.Kind == "issue" {
		pull, err := s.ensurePullRequest(ctx, fc, ticket, branch, base)
		if err != nil {
			_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output)
			return err
		}
		if pull != nil {
			prText = fmt.Sprintf("\n\nPull request: %s", firstNonEmpty(pull.HTMLURL, fmt.Sprintf("#%d", pull.NumberValue())))
		}
	}

	comment := successComment(branch, committed, result.Output, prText)
	if err := s.postSuccess(ctx, fc, ticket, comment); err != nil {
		return err
	}

	s.logger.Info("ticket workflow completed", "ticket", ticket.Ref(), "branch", branch, "committed", committed)
	return nil
}

// routeToken returns the Forgejo token for the given Forgejo client by looking up which route it belongs to.
// Falls back to the global token.
func (s *Service) routeToken(ticket forgejo.Ticket, fc Forgejo) string {
	if s.forgejoClients != nil {
		for mention, client := range s.forgejoClients {
			if client == fc {
				for _, route := range s.cfg.Agents {
					if strings.ToLower(route.Mention) == mention && route.Token != "" {
						return route.Token
					}
				}
			}
		}
	}
	return s.cfg.ForgejoToken
}

func (s *Service) ensurePullRequest(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, branch, base string) (*forgejo.PullRequest, error) {
	existing, err := fc.FindOpenPullRequest(ctx, ticket.Owner, ticket.Repo, branch)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		return existing, nil
	}

	return fc.CreatePullRequest(ctx, ticket.Owner, ticket.Repo, forgejo.CreatePullRequestRequest{
		Base:  base,
		Head:  branch,
		Title: "forge-ai: " + ticket.Title,
		Body:  fmt.Sprintf("Automated work for %s #%d.\n\n%s", ticket.Kind, ticket.Number, ticket.HTMLURL),
	})
}

func (s *Service) postFailure(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, err error) error {
	return s.postFailureWithOutput(ctx, fc, ticket, err, "")
}

func (s *Service) postFailureWithOutput(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, err error, output string) error {
	body := "forge-ai failed: `" + sanitizeInline(err.Error()) + "`"
	if strings.TrimSpace(output) != "" {
		body += "\n\nLast agent output:\n\n```text\n" + fence(output) + "\n```"
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func (s *Service) postStartAckWith(ctx context.Context, fc Forgejo, ticket forgejo.Ticket) error {
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

func (s *Service) postStart(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, branch string) error {
	if ticket.CommentID != 0 || strings.TrimSpace(ticket.Instruction) != "" {
		return nil
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, "forge-ai: starting work on `"+branch+"`.")
}

func (s *Service) postSuccess(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, body string) error {
	if ticket.CommentID != 0 {
		if err := fc.CreateCommentReaction(ctx, ticket.Owner, ticket.Repo, ticket.CommentID, "+1"); err == nil {
			return nil
		}
		return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
	}
	return fc.CreateIssueComment(ctx, ticket.Owner, ticket.Repo, ticket.Number, body)
}

func prompt(ticket forgejo.Ticket, branch, base string, allowGit bool, toolHints string) string {
	gitPolicy := `The repository is already checked out on the prepared branch above. Do not run git commands. Make file changes only; the outer forge-ai service will commit and push the prepared branch.`
	if allowGit {
		gitPolicy = `The repository is already checked out on the prepared branch above. Stay on that branch. You may use git status, git diff, git add, and git commit on the current branch only. Do not create, switch, reset, rebase, merge, or delete branches. Do not push; the outer forge-ai service will push the prepared branch and post back to Forgejo.`
	}
	var toolSection string
	if strings.TrimSpace(toolHints) != "" {
		toolSection = "Available tools:\n" + strings.TrimSpace(toolHints) + "\n\n"
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

%sBefore making any changes, first read .forge-ai/instructions.md if it exists, then explore the repository to understand its structure and existing code. If an AGENTS.md or CLAUDE.md exists, read and follow its instructions too. Then implement the requested change. If you cannot complete the requested change, explain the blocker in your final response. If all is good you may write a short summary as final response.

When done, write a short conventional-commits commit message to the file ".forge-ai-commit-msg" in the repository root. One line only. Do not commit or push.`,
		ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number, branch, base, ticket.HTMLURL, ticket.Title, ticket.Body, strings.TrimSpace(ticket.Instruction), gitPolicy, toolSection)
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

func (s *Service) logWorkspaceFiles(workdir, label string) {
	entries, err := os.ReadDir(workdir)
	if err != nil {
		s.logger.Info("workspace listing failed", "label", label, "error", err)
		return
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	s.logger.Info("workspace contents", "label", label, "workdir", workdir, "files", strings.Join(names, ", "))
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
