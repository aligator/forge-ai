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
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/gitops"
	"codeberg.org/forge-ai/internal/runstore"
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
	Run(context.Context, string, string, string) (agent.Result, error)
}

type optionsAgent interface {
	RunWithOptions(context.Context, agent.RunOptions) (agent.Result, error)
}

type Options struct {
	Config         config.Config
	Forgejo        Forgejo
	ForgejoClients map[string]Forgejo // mention (lowercase) → per-route client; falls back to Forgejo
	Git            Git
	Agents         map[string]Agent // mention (lowercase) → runner
	RunStore       runstore.RunStore
	Logger         *slog.Logger
}

type Service struct {
	cfg            config.Config
	forgejo        Forgejo
	forgejoClients map[string]Forgejo
	git            Git
	agents         map[string]Agent
	runStore       runstore.RunStore
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
		runStore:       options.RunStore,
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
	branch := branchForTicket(s.cfg, ticket)
	base := firstNonEmpty(ticket.BaseBranch, ticket.DefaultBranch, "main")
	run, err := s.createRun(ctx, payload, ticket, mention, branch, base)
	if err != nil {
		return err
	}

	if err := s.postStartAckWith(ctx, fc, ticket); err != nil {
		s.logger.Warn("post start acknowledgement failed", "comment_id", ticket.CommentID, "error", err)
	}

	s.semaphore <- struct{}{}
	defer func() { <-s.semaphore }()

	return s.run(ctx, fc, ticket, ag, run)
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

func (s *Service) run(ctx context.Context, fc Forgejo, ticket forgejo.Ticket, ag Agent, run runstore.Run) error {
	branch := firstNonEmpty(run.Branch, branchForTicket(s.cfg, ticket))
	base := firstNonEmpty(run.BaseBranch, ticket.BaseBranch, ticket.DefaultBranch, "main")

	s.logger.Info("starting ticket workflow", "ticket", ticket.Ref(), "repo", ticket.Owner+"/"+ticket.Repo, "branch", branch)
	s.markRunRunning(ctx, run.ID)
	if err := s.postStart(ctx, fc, ticket, branch); err != nil {
		s.logger.Warn("post start comment failed", "error", err)
	}

	token := s.routeToken(ticket, fc)
	cloneURL := rewriteCloneURL(ticket.CloneURL, s.cfg.CloneURLBase)
	workdir, err := s.git.Prepare(ctx, s.cfg.WorkspaceDir, cloneURL, token, ticket.Owner, ticket.Repo, branch, base)
	if err != nil {
		s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = s.postFailure(ctx, fc, ticket, err)
		return err
	}
	s.addRunEvent(ctx, run.ID, "workspace_ready", "workspace prepared")

	s.logger.Info("workspace ready", "workdir", workdir, "branch", branch)
	s.logWorkspaceFiles(workdir, "before agent run")
	sessionID := sessionIDFromInstruction(ticket.Instruction, s.cfg.Agents)
	result, agentErr := s.runAgent(ctx, ag, workdir, prompt(ticket, branch, base, s.cfg.AgentAllowGit, s.cfg.AgentToolHints), sessionID, run.ID)
	s.logWorkspaceFiles(workdir, "after agent run")
	if agentErr != nil {
		err := fmt.Errorf("agent failed: %w", agentErr)
		s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	s.addRunEvent(ctx, run.ID, "agent_finished", "agent completed")

	commitMsg := readAndRemoveCommitMsg(workdir)
	if commitMsg == "" {
		commitMsg = fmt.Sprintf("forge-ai: work on %s #%d", ticket.Kind, ticket.Number)
	}
	committed, err := s.git.CommitIfDirty(ctx, workdir, commitMsg)
	if err != nil {
		s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	s.addRunEvent(ctx, run.ID, "commit_checked", fmt.Sprintf("committed=%t", committed))

	if err := s.git.Push(ctx, workdir, branch); err != nil {
		s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
		return err
	}
	s.addRunEvent(ctx, run.ID, "pushed", "branch pushed")
	defer s.removeWorkspace(workdir)

	prText := ""
	if s.cfg.CreatePR && ticket.Kind == "issue" {
		pull, err := s.ensurePullRequest(ctx, fc, ticket, branch, base)
		if err != nil {
			s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
			_ = s.postFailureWithOutput(ctx, fc, ticket, err, result.Output, result.SessionID)
			return err
		}
		if pull != nil {
			prText = fmt.Sprintf("\n\nPull request: %s", firstNonEmpty(pull.HTMLURL, fmt.Sprintf("#%d", pull.NumberValue())))
			s.addRunLink(ctx, run.ID, "pull_request", pull.HTMLURL, fmt.Sprintf("PR #%d", pull.NumberValue()))
		}
	}

	comment := successComment(branch, committed, result.SessionID, prText)
	if err := s.postSuccess(ctx, fc, ticket, comment); err != nil {
		s.finishRun(ctx, run.ID, runstore.StatusFailed, err)
		return err
	}

	s.finishRun(ctx, run.ID, runstore.StatusSuccess, nil)
	s.logger.Info("ticket workflow completed", "ticket", ticket.Ref(), "branch", branch, "committed", committed)
	return nil
}

func (s *Service) createRun(ctx context.Context, payload forgejo.WebhookPayload, ticket forgejo.Ticket, mention, branch, base string) (runstore.Run, error) {
	if s.runStore == nil {
		return runstore.Run{Branch: branch, BaseBranch: base}, nil
	}
	agentType := ""
	for _, route := range s.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			agentType = route.Agent.Type
			break
		}
	}
	createdBy := ""
	if payload.Sender != nil {
		createdBy = payload.Sender.Handle()
	}
	run, err := s.runStore.CreateRun(ctx, runstore.CreateRunInput{
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
	s.addRunEvent(ctx, run.ID, "queued", "webhook accepted")
	s.addRunLink(ctx, run.ID, "ticket", ticket.HTMLURL, ticket.Ref())
	return run, nil
}

func (s *Service) markRunRunning(ctx context.Context, runID string) {
	if s.runStore == nil || runID == "" {
		return
	}
	if err := s.runStore.UpdateRunStatus(ctx, runID, runstore.StatusRunning, time.Time{}, ""); err != nil {
		s.logger.Warn("record run status failed", "run_id", runID, "status", runstore.StatusRunning, "error", err)
	}
	s.addRunEvent(ctx, runID, "running", "workflow started")
}

func (s *Service) finishRun(ctx context.Context, runID string, status runstore.Status, runErr error) {
	if s.runStore == nil || runID == "" {
		return
	}
	message := ""
	if runErr != nil {
		message = runErr.Error()
	}
	if err := s.runStore.UpdateRunStatus(ctx, runID, status, timeNow(), message); err != nil {
		s.logger.Warn("record run status failed", "run_id", runID, "status", status, "error", err)
	}
	s.addRunEvent(ctx, runID, string(status), message)
}

func (s *Service) runAgent(ctx context.Context, ag Agent, workdir, prompt, sessionID, runID string) (agent.Result, error) {
	if streaming, ok := ag.(optionsAgent); ok {
		result, err := streaming.RunWithOptions(ctx, agent.RunOptions{
			Workdir:   workdir,
			Prompt:    prompt,
			SessionID: sessionID,
			Output:    runLogWriter{ctx: ctx, store: s.runStore, runID: runID, logger: s.logger},
		})
		s.recordAgentSession(ctx, runID, result.SessionID)
		return result, err
	}
	result, err := ag.Run(ctx, workdir, prompt, sessionID)
	s.recordAgentResult(ctx, runID, result)
	return result, err
}

func (s *Service) recordAgentResult(ctx context.Context, runID string, result agent.Result) {
	s.recordAgentSession(ctx, runID, result.SessionID)
	if s.runStore == nil || runID == "" || result.Output == "" {
		return
	}
	if err := s.runStore.AddLogChunk(ctx, runstore.LogChunkInput{
		RunID:  runID,
		Time:   timeNow(),
		Stream: "combined",
		Chunk:  result.Output,
	}); err != nil {
		s.logger.Warn("record run log failed", "run_id", runID, "error", err)
	}
}

func (s *Service) recordAgentSession(ctx context.Context, runID, sessionID string) {
	if s.runStore == nil || runID == "" {
		return
	}
	if strings.TrimSpace(sessionID) != "" {
		if err := s.runStore.SetSessionID(ctx, runID, sessionID); err != nil {
			s.logger.Warn("record run session failed", "run_id", runID, "error", err)
		}
	}
}

type runLogWriter struct {
	ctx    context.Context
	store  runstore.RunStore
	runID  string
	logger *slog.Logger
}

func (w runLogWriter) WriteOutput(chunk agent.OutputChunk) error {
	if w.store == nil || w.runID == "" || chunk.Chunk == "" {
		return nil
	}
	if chunk.Time.IsZero() {
		chunk.Time = timeNow()
	}
	err := w.store.AddLogChunk(w.ctx, runstore.LogChunkInput{
		RunID:  w.runID,
		Time:   chunk.Time,
		Stream: string(chunk.Stream),
		Chunk:  chunk.Chunk,
	})
	if err != nil && w.logger != nil {
		w.logger.Warn("record run log failed", "run_id", w.runID, "stream", chunk.Stream, "error", err)
	}
	return err
}

func (s *Service) addRunEvent(ctx context.Context, runID, eventType, message string) {
	if s.runStore == nil || runID == "" {
		return
	}
	if err := s.runStore.AddEvent(ctx, runstore.EventInput{
		RunID:   runID,
		Time:    timeNow(),
		Type:    eventType,
		Message: message,
	}); err != nil {
		s.logger.Warn("record run event failed", "run_id", runID, "type", eventType, "error", err)
	}
}

func (s *Service) addRunLink(ctx context.Context, runID, linkType, url, label string) {
	if s.runStore == nil || runID == "" || strings.TrimSpace(url) == "" {
		return
	}
	if err := s.runStore.AddLink(ctx, runstore.LinkInput{
		RunID: runID,
		Type:  linkType,
		URL:   url,
		Label: label,
	}); err != nil {
		s.logger.Warn("record run link failed", "run_id", runID, "type", linkType, "error", err)
	}
}

var timeNow = func() time.Time {
	return time.Now().UTC()
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

Trigger comment = main task. Body = context. Before plan: read issue/PR comments via Forgejo MCP. API fallback only if MCP fails. Apply relevant later comments. Need to communicate with user? Post a Forgejo comment via Forgejo MCP. You may post short progress updates or questions whenever useful.
%s

%sFirst: read .forge-ai/instructions.md. Inspect relevant files only. Skip configs unless needed. Current repo only. No parent/sibling dirs. Read relevant AGENTS.md/CLAUDE.md. Never print secrets/full env. Prefix shell cmds with 'rtk'. Implement. Blocked? explain. Done? short summary.

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
