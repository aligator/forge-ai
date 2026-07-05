package service

import (
	"context"
	"errors"
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
	Logger         *slog.Logger
}

type Service struct {
	cfg            config.Config
	forgejo        Forgejo
	forgejoClients map[string]Forgejo
	git            Git
	agents         map[string]Agent
	runtime        *appruntime.Runtime
	logger         *slog.Logger
}

func New(options Options) *Service {
	rt := options.Runtime
	if rt == nil {
		rt = appruntime.New(options.Config.MaxConcurrent)
	}
	return &Service{
		cfg:            options.Config,
		forgejo:        options.Forgejo,
		forgejoClients: options.ForgejoClients,
		git:            options.Git,
		agents:         options.Agents,
		runtime:        rt,
		logger:         options.Logger,
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

	mention, ag := s.findAgent(ticket.Instruction)
	fc := s.forgejoFor(mention)
	route := s.routeForMention(mention)
	branch := branchForTicket(s.cfg, ticket)
	spec := appruntime.RunSpec{
		TicketRef: ticket.Ref(),
		Branch:    branch,
		Owner:     ticket.Owner,
		Repo:      ticket.Repo,
		Kind:      ticket.Kind,
		Number:    ticket.Number,
	}

	err := s.runtime.SubmitWebhookRun(ctx, spec, func(runCtx context.Context) error {
		if err := s.postStartAckWith(runCtx, fc, ticket); err != nil {
			s.logger.Warn("post start acknowledgement failed", "comment_id", ticket.CommentID, "error", err)
		}
		return s.run(runCtx, fc, ticket, ag, route.Git)
	})
	switch {
	case errors.Is(err, appruntime.ErrTicketActive):
		s.logger.Info("ignored webhook, ticket already active", "ticket", ticket.Ref())
		return nil
	case errors.Is(err, appruntime.ErrBranchActive):
		s.logger.Info("ignored webhook, branch already active", "ticket", ticket.Ref(), "branch", branch)
		return nil
	default:
		return err
	}
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

func (s *Service) routeForMention(mention string) config.AgentRoute {
	for _, route := range s.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			return route
		}
	}
	return config.AgentRoute{Git: s.cfg.Git.GitIdentity}
}

func (s *Service) run(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, ag Agent, identity config.GitIdentity) error {
	branch := branchForTicket(s.cfg, ticket)
	base := firstNonEmpty(ticket.BaseBranch, ticket.DefaultBranch, "main")

	s.logger.Info("starting ticket workflow", "ticket", ticket.Ref(), "repo", ticket.Owner+"/"+ticket.Repo, "branch", branch)
	if err := s.postStart(ctx, fc, ticket, branch); err != nil {
		s.logger.Warn("post start comment failed", "error", err)
	}

	token := s.routeToken(ticket, fc)
	cloneURL := rewriteCloneURL(ticket.CloneURL, s.cfg.CloneURLBase)
	workdir, err := s.git.Prepare(ctx, s.cfg.WorkspaceDir, cloneURL, token, ticket.Owner, ticket.Repo, branch, base, identity)
	if err != nil {
		_ = s.postFailure(ctx, fc, ticket, err)
		return err
	}

	s.logger.Info("workspace ready", "workdir", workdir, "branch", branch)
	s.logWorkspaceFiles(workdir, "before agent run")
	sessionID := sessionIDFromInstruction(ticket.Instruction, s.cfg.Agents)
	result, agentErr := ag.Run(ctx, workdir, prompt(ticket, branch, base, s.cfg.AgentAllowGit, s.cfg.AgentToolHints), sessionID)
	s.logWorkspaceFiles(workdir, "after agent run")
	if agentErr != nil {
		err := fmt.Errorf("agent failed: %w", agentErr)
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}

	commitMsg := readAndRemoveCommitMsg(workdir)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("forge-ai: work on %s #%d", ticket.Kind, ticket.Number)
	}
	committed, err := s.git.CommitIfDirty(ctx, workdir, commitMsg)
	if err != nil {
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}

	if err := s.git.Push(ctx, workdir, branch); err != nil {
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	defer s.removeWorkspace(workdir)

	prText := ""
	if s.cfg.CreatePR && ticket.Kind == "issue" {
		pull, err := s.ensurePullRequest(ctx, fc, ticket, branch, base)
		if err != nil {
			_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
			return err
		}
		if pull != nil {
			prText = fmt.Sprintf("\n\nPull request: %s", firstNonEmpty(pull.HTMLURL, fmt.Sprintf("#%d", pull.NumberValue())))
		}
	}

	comment := successComment(branch, committed, result.SessionID, prText)
	if err := s.postSuccess(ctx, fc, ticket, comment); err != nil {
		return err
	}

	s.logger.Info("ticket workflow completed", "ticket", ticket.Ref(), "branch", branch, "committed", committed)
	return nil
}

func (s *Service) removeWorkspace(workdir string) {
	if workdir == "" || filepath.Clean(workdir) == filepath.Clean(s.cfg.WorkspaceDir) {
		s.logger.Warn("skip unsafe workspace cleanup", "workdir", workdir)
		return
	}
	if err := os.RemoveAll(workdir); err != nil {
		s.logger.Warn("workspace cleanup failed", "workdir", workdir, "error", err)
		return
	}
	s.logger.Info("workspace removed", "workdir", workdir)
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
		if existing.Base.Ref != base {
			return fc.UpdatePullRequest(ctx, ticket.Owner, ticket.Repo, existing.NumberValue(), forgejo.UpdatePullRequestRequest{
				Base: base,
			})
		}
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
	return s.postFailureWithOutput(ctx, fc, ticket, err, "", "")
}

func (s *Service) postFailureWithOutput(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, err error, output, sessionID string) error {
	body := "forge-ai failed: `" + sanitizeInline(err.Error()) + "`"
	if strings.TrimSpace(sessionID) != "" {
		body += "\n\nAgent session: `" + sanitizeInline(sessionID) + "`"
	}
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

func prompt(ticket forgejo.Ticket, branch, base string, allowGit bool, toolHints string) string {
	gitPolicy := `Repo already on branch. No git cmds. Edit files only; forge-ai commits+pushes.`
	if allowGit {
		gitPolicy = `Repo already on branch. Stay there. Allowed: git status, diff, add, commit. Forbidden: create/switch/reset/rebase/merge/delete branches, push. forge-ai pushes+posts.`
	}
	var toolSection string
	if strings.TrimSpace(toolHints) != "" {
		toolSection = "Tools:\n" + strings.TrimSpace(toolHints) + "\n\n"
	}
	return fmt.Sprintf(`Work in cloned Forgejo repo.

Repo: %s/%s
Ticket: %s #%d
Branch: %s
Base: %s
URL: %s

Forgejo API: use FORGEJO_URL env. No localhost unless FORGEJO_URL says localhost.

Title:
%s

Body:
%s

Trigger comment:
%s

Trigger comment = main task. Body = context. If the user asks clear questions or asks for analysis/advice, answer only; do not implement unless the user asks for code changes. Before plan: read issue/PR comments via Forgejo MCP. API fallback only if MCP fails. Apply relevant later comments. Need to communicate with user? Post a Forgejo comment via Forgejo MCP. You may post short progress updates or questions whenever useful.
%s

%sFirst: read .forge-ai/instructions.md. Inspect relevant files only. Skip configs unless needed. Current repo only. No parent/sibling dirs. Read relevant AGENTS.md/CLAUDE.md. Never print secrets/full env. Prefix shell cmds with 'rtk'. Keep changes minimal and focused. Preserve existing style and architecture unless instructed otherwise. If an attempt failed, clean up abandoned code before moving on. Bugfixes must fix the root cause when possible; do not patch only symptoms. Structure code cleanly from the start with sensible architecture, not everything in one file. Implement. Blocked? explain. Done? short summary.

Done: write one-line conventional commit msg to ".forge-ai-commit-msg". No commit. No push.`,
		ticket.Owner, ticket.Repo, ticket.Kind, ticket.Number, branch, base, ticket.HTMLURL, ticket.Title, ticket.Body, strings.TrimSpace(ticket.Instruction), gitPolicy, toolSection)
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

func sessionIDFromInstruction(instruction string, routes []config.AgentRoute) string {
	lower := strings.ToLower(instruction)
	for _, route := range routes {
		mention := strings.ToLower(route.Mention)
		idx := strings.Index(lower, mention)
		if idx < 0 {
			continue
		}
		rest := strings.TrimSpace(instruction[idx+len(route.Mention):])
		if rest == "" {
			return ""
		}
		token := strings.Fields(rest)[0]
		if validSessionIDToken(token) {
			return token
		}
	}
	return ""
}

func validSessionIDToken(token string) bool {
	token = strings.Trim(token, "`'\"")
	if len(token) < 6 {
		return false
	}
	for _, r := range token {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' || r == ':' {
			continue
		}
		return false
	}
	if strings.ContainsAny(token, "-_:") {
		return true
	}
	return len(token) >= 16
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
