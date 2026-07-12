package service

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
)

type stubAgent struct {
	name   string
	result agent.Result
	err    error
}

func (a *stubAgent) Run(_ context.Context, _, _, _ string) (agent.Result, error) {
	return a.result, a.err
}

type spyAgent struct {
	onRun func()
}

func (a *spyAgent) Run(_ context.Context, _, _, _ string) (agent.Result, error) {
	if a.onRun != nil {
		a.onRun()
	}
	return agent.Result{}, nil
}

type streamingStubAgent struct {
	result agent.Result
	chunks []agent.OutputChunk
	err    error
}

func (a *streamingStubAgent) Run(context.Context, string, string, string) (agent.Result, error) {
	panic("Run should not be called")
}

func (a *streamingStubAgent) RunWithOptions(_ context.Context, options agent.RunOptions) (agent.Result, error) {
	for _, chunk := range a.chunks {
		if options.Output != nil {
			if err := options.Output.WriteOutput(chunk); err != nil {
				return agent.Result{}, err
			}
		}
	}
	return a.result, a.err
}

type recordingForgejo struct {
	commentBody            string
	reactionCommentID      int64
	reactionContent        string
	reactionErr            error
	reviewComments         []string
	openPullRequest        *forgejo.PullRequest
	openPullRequests       []*forgejo.PullRequest
	createPullRequest      forgejo.CreatePullRequestRequest
	createPullRequestErr   error
	updatePullRequest      forgejo.UpdatePullRequestRequest
	updatePullRequestIndex int
}

func (f *recordingForgejo) GetLatestPullReviewComments(_ context.Context, _, _ string, _ int) ([]forgejo.Comment, error) {
	comments := make([]forgejo.Comment, 0, len(f.reviewComments))
	for _, body := range f.reviewComments {
		comments = append(comments, forgejo.Comment{Body: body})
	}
	return comments, nil
}

func (f *recordingForgejo) CreateIssueComment(_ context.Context, _, _ string, _ int, body string) error {
	f.commentBody = body
	return nil
}

func (f *recordingForgejo) CreateCommentReaction(_ context.Context, _ string, _ string, commentID int64, content string) error {
	f.reactionCommentID = commentID
	f.reactionContent = content
	return f.reactionErr
}

func (f *recordingForgejo) FindOpenPullRequest(context.Context, string, string, string) (*forgejo.PullRequest, error) {
	if len(f.openPullRequests) > 0 {
		pull := f.openPullRequests[0]
		f.openPullRequests = f.openPullRequests[1:]
		return pull, nil
	}
	return f.openPullRequest, nil
}

func (f *recordingForgejo) CreatePullRequest(_ context.Context, _ string, _ string, request forgejo.CreatePullRequestRequest) (*forgejo.PullRequest, error) {
	f.createPullRequest = request
	if f.createPullRequestErr != nil {
		return nil, f.createPullRequestErr
	}
	return nil, nil
}

func (f *recordingForgejo) UpdatePullRequest(_ context.Context, _ string, _ string, index int, request forgejo.UpdatePullRequestRequest) (*forgejo.PullRequest, error) {
	f.updatePullRequestIndex = index
	f.updatePullRequest = request
	return f.openPullRequest, nil
}

type recordingGit struct {
	workdir  string
	identity config.GitIdentity
}

func (g *recordingGit) Prepare(_ context.Context, _, _, _, _, _, _, _ string, identity config.GitIdentity) (string, error) {
	g.identity = identity
	return g.workdir, nil
}

func (g *recordingGit) CommitIfDirty(context.Context, string, string) (bool, error) {
	return true, nil
}

func (g *recordingGit) Push(context.Context, string, string) error {
	return nil
}

type recordingRunStore struct {
	mu       sync.Mutex
	runs     []runstore.Run
	statuses []runstore.Status
	events   []runstore.EventInput
	logs     []runstore.LogChunkInput
	links    []runstore.LinkInput
	audit    []runstore.AuditEventInput
}

func (s *recordingRunStore) GetRun(_ context.Context, id string) (runstore.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.runs {
		if run.ID == id {
			return run, nil
		}
	}
	return runstore.Run{}, sql.ErrNoRows
}

func (s *recordingRunStore) CreateRun(_ context.Context, in runstore.CreateRunInput) (runstore.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	id := in.ID
	if id == "" {
		id = fmt.Sprintf("run-%d", len(s.runs)+1)
	}
	run := runstore.Run{
		ID:           id,
		Kind:         in.Kind,
		Status:       in.Status,
		Owner:        in.Owner,
		Repo:         in.Repo,
		TicketKind:   in.TicketKind,
		TicketNumber: in.TicketNumber,
		Branch:       in.Branch,
		BaseBranch:   in.BaseBranch,
		AgentMention: in.AgentMention,
		AgentType:    in.AgentType,
		SessionID:    in.SessionID,
		ParentRunID:  in.ParentRunID,
		StartedAt:    in.StartedAt,
		CreatedBy:    in.CreatedBy,
	}
	s.runs = append(s.runs, run)
	return run, nil
}

func (s *recordingRunStore) UpdateRunStatus(_ context.Context, id string, status runstore.Status, finishedAt time.Time, message string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID == id {
			s.runs[i].Status = status
			s.runs[i].FinishedAt = finishedAt
			s.runs[i].Error = message
		}
	}
	s.statuses = append(s.statuses, status)
	return nil
}

func (s *recordingRunStore) SetSessionID(_ context.Context, id, sessionID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.runs {
		if s.runs[i].ID == id {
			s.runs[i].SessionID = sessionID
		}
	}
	return nil
}

func (s *recordingRunStore) AddEvent(_ context.Context, in runstore.EventInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, in)
	return nil
}

func (s *recordingRunStore) AddLogChunk(_ context.Context, in runstore.LogChunkInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logs = append(s.logs, in)
	return nil
}

func (s *recordingRunStore) AddLink(_ context.Context, in runstore.LinkInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.links = append(s.links, in)
	return nil
}

func (s *recordingRunStore) AddAuditEvent(_ context.Context, in runstore.AuditEventInput) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.audit = append(s.audit, in)
	return nil
}

func (s *recordingRunStore) hasEvent(eventType string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, event := range s.events {
		if event.Type == eventType {
			return true
		}
	}
	return false
}

func (s *recordingRunStore) hasLink(linkType, url string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, link := range s.links {
		if link.Type == linkType && link.URL == url {
			return true
		}
	}
	return false
}
