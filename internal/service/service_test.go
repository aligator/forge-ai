package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
)

func TestBranchForPullRequestUsesHeadBranch(t *testing.T) {
	got := branchForTicket(config.Config{BranchPrefix: "forge-ai"}, forgejo.Ticket{
		Owner:      "ac",
		Repo:       "demo",
		Kind:       "pr",
		Number:     8,
		HeadBranch: "feature/login",
	})
	if got != "feature/login" {
		t.Fatalf("branchForTicket() = %q", got)
	}
}

func TestBranchForIssueUsesManagedBranch(t *testing.T) {
	got := branchForTicket(config.Config{BranchPrefix: "forge-ai"}, forgejo.Ticket{
		Owner:  "ac",
		Repo:   "demo",
		Kind:   "issue",
		Number: 8,
	})
	want := "forge-ai/ac/demo/issue-8"
	if got != want {
		t.Fatalf("branchForTicket() = %q, want %q", got, want)
	}
}

func TestRewriteCloneURL(t *testing.T) {
	got := rewriteCloneURL("http://localhost:3000/ac/demo.git", "http://forgejo:3000")
	want := "http://forgejo:3000/ac/demo.git"
	if got != want {
		t.Fatalf("rewriteCloneURL() = %q, want %q", got, want)
	}
}

func TestPromptTellsAgentToCommunicateViaForgejoMCP(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai hello",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"read issue/PR comments via Forgejo MCP",
		"Post a Forgejo comment via Forgejo MCP",
		"short progress updates",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptTellsAgentWhenNotToImplement(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai what would you change?",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"If the user asks clear questions or asks for analysis/advice, answer only",
		"do not implement unless the user asks for code changes",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestPromptTellsAgentToKeepImplementationClean(t *testing.T) {
	got := prompt(forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Kind:        "issue",
		Number:      1,
		Instruction: "@forge-ai implement",
	}, "forge-ai/ac/demo/issue-1", "main", false, "")

	for _, want := range []string{
		"Keep changes minimal and focused",
		"Preserve existing style and architecture",
		"clean up abandoned code",
		"fix the root cause when possible",
		"Structure code cleanly from the start",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("prompt() missing %q in:\n%s", want, got)
		}
	}
}

func TestShouldRunOnlyForMention(t *testing.T) {
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "forge-ai", Mention: "@forge-ai"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
	})

	if !svc.shouldRun(forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-user"}}, forgejo.Ticket{Instruction: "@forge-ai say hello"}) {
		t.Fatal("expected mention to trigger")
	}

	if svc.shouldRun(forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-user"}}, forgejo.Ticket{Labels: []forgejo.Label{{Name: "ai"}}}) {
		t.Fatal("expected label alone not to trigger")
	}

	if svc.shouldRun(forgejo.WebhookPayload{Sender: &forgejo.User{Login: "forge-ai"}}, forgejo.Ticket{Instruction: "@forge-ai done"}) {
		t.Fatal("expected bot mention not to trigger")
	}
}

func TestShouldRunForAnyConfiguredMention(t *testing.T) {
	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex"},
				{User: "claude", Mention: "@claude"},
				{User: "opencode", Mention: "@opencode"},
			},
			ForgejoBootstrapUser: "forge-ai",
			MaxConcurrent:        1,
		},
	})

	for _, mention := range []string{"@codex", "@claude", "@opencode"} {
		if !svc.shouldRun(forgejo.WebhookPayload{Sender: &forgejo.User{Login: "user"}}, forgejo.Ticket{Instruction: mention + " do something"}) {
			t.Fatalf("expected %q to trigger", mention)
		}
	}

	if svc.shouldRun(forgejo.WebhookPayload{Sender: &forgejo.User{Login: "user"}}, forgejo.Ticket{Instruction: "@other do something"}) {
		t.Fatal("expected unknown mention not to trigger")
	}
}

func TestFindAgentPicksCorrectRunner(t *testing.T) {
	codexRunner := &stubAgent{name: "codex"}
	claudeRunner := &stubAgent{name: "claude"}

	svc := New(Options{
		Config: config.Config{
			Agents: []config.AgentRoute{
				{User: "codex", Mention: "@codex"},
				{User: "claude", Mention: "@claude"},
			},
			MaxConcurrent: 1,
		},
		Agents: map[string]Agent{
			"@codex":  codexRunner,
			"@claude": claudeRunner,
		},
	})

	if _, got := svc.findAgent("@codex please fix this"); got != codexRunner {
		t.Fatal("expected codex runner")
	}
	if _, got := svc.findAgent("@claude please fix this"); got != claudeRunner {
		t.Fatal("expected claude runner")
	}
}

func TestSessionIDFromInstructionUsesTokenAfterMention(t *testing.T) {
	routes := []config.AgentRoute{{Mention: "@claude"}}
	got := sessionIDFromInstruction("@claude session-123 continue work", routes)
	if got != "session-123" {
		t.Fatalf("sessionIDFromInstruction() = %q, want session-123", got)
	}
}

func TestSessionIDFromInstructionIgnoresNormalTaskText(t *testing.T) {
	routes := []config.AgentRoute{{Mention: "@claude"}}
	got := sessionIDFromInstruction("@claude fix bug", routes)
	if got != "" {
		t.Fatalf("sessionIDFromInstruction() = %q, want empty", got)
	}
}

func TestSuccessCommentIncludesSessionID(t *testing.T) {
	got := successComment("work", true, "session-123", "")
	if !strings.Contains(got, "Agent session: `session-123`") {
		t.Fatalf("successComment() missing session id:\n%s", got)
	}
}

func TestSuccessCommentOmitsAgentOutput(t *testing.T) {
	got := successComment("work", true, "session-123", "")
	if strings.Contains(got, "Last agent output:") {
		t.Fatalf("successComment() includes agent output:\n%s", got)
	}
}

func TestPostStartAckReactsToComment(t *testing.T) {
	forge := &recordingForgejo{}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postStartAckWith(context.Background(), forge, forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Number:      1,
		CommentID:   42,
		Instruction: "@forge-ai hello",
	})
	if err != nil {
		t.Fatalf("postStartAck() error = %v", err)
	}
	if forge.reactionContent != "eyes" || forge.reactionCommentID != 42 {
		t.Fatalf("reaction = %q on %d, want eyes on 42", forge.reactionContent, forge.reactionCommentID)
	}
	if forge.commentBody != "" {
		t.Fatalf("commentBody = %q, want no comment", forge.commentBody)
	}
}

func TestPostStartAckRepliesEyesWithoutCommentID(t *testing.T) {
	forge := &recordingForgejo{}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postStartAckWith(context.Background(), forge, forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Number:      1,
		Instruction: "@forge-ai hello",
	})
	if err != nil {
		t.Fatalf("postStartAck() error = %v", err)
	}
	if forge.commentBody != ":eyes:" {
		t.Fatalf("commentBody = %q, want :eyes:", forge.commentBody)
	}
	if forge.reactionContent != "" {
		t.Fatalf("reactionContent = %q, want no reaction", forge.reactionContent)
	}
}

func TestPostStartAckRepliesEyesWhenReactionFails(t *testing.T) {
	forge := &recordingForgejo{reactionErr: errors.New("comment does not exist")}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postStartAckWith(context.Background(), forge, forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Number:      1,
		CommentID:   42,
		Instruction: "@forge-ai hello",
	})
	if err != nil {
		t.Fatalf("postStartAck() error = %v", err)
	}
	if forge.commentBody != ":eyes:" {
		t.Fatalf("commentBody = %q, want :eyes:", forge.commentBody)
	}
}

func TestPostSuccessPostsCommentWhenReactionFails(t *testing.T) {
	forge := &recordingForgejo{reactionErr: errors.New("comment does not exist")}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postSuccess(context.Background(), forge, forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Number:      1,
		CommentID:   42,
		Instruction: "@forge-ai hello",
	}, "done")
	if err != nil {
		t.Fatalf("postSuccess() error = %v", err)
	}
	if forge.commentBody != "done" {
		t.Fatalf("commentBody = %q, want done", forge.commentBody)
	}
}

func TestPostSuccessPostsCommentWithSessionID(t *testing.T) {
	forge := &recordingForgejo{}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postSuccess(context.Background(), forge, forgejo.Ticket{
		Owner:       "ac",
		Repo:        "demo",
		Number:      1,
		CommentID:   42,
		Instruction: "@codex hello",
	}, "done\n\nAgent session: `019f2cd5-9288-73b1-bf76-8ac3f2e8ce7a`")
	if err != nil {
		t.Fatalf("postSuccess() error = %v", err)
	}
	if !strings.Contains(forge.commentBody, "019f2cd5-9288-73b1-bf76-8ac3f2e8ce7a") {
		t.Fatalf("commentBody = %q, want session id", forge.commentBody)
	}
	if forge.reactionContent != "" {
		t.Fatalf("reactionContent = %q, want no reaction", forge.reactionContent)
	}
}

func TestEnsurePullRequestUsesIssueBaseBranch(t *testing.T) {
	forge := &recordingForgejo{}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	_, err := svc.ensurePullRequest(context.Background(), forge, forgejo.Ticket{
		Owner:  "ac",
		Repo:   "demo",
		Kind:   "issue",
		Number: 8,
		Title:  "Fix it",
	}, "forge-ai/ac/demo/issue-8", "release/1.2")
	if err != nil {
		t.Fatalf("ensurePullRequest() error = %v", err)
	}
	if forge.createPullRequest.Base != "release/1.2" {
		t.Fatalf("CreatePullRequest base = %q, want release/1.2", forge.createPullRequest.Base)
	}
}

func TestEnsurePullRequestRetargetsExistingPullRequest(t *testing.T) {
	forge := &recordingForgejo{
		openPullRequest: &forgejo.PullRequest{
			Index: 4,
			Base:  forgejo.PullRequestBranch{Ref: "main"},
		},
	}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	_, err := svc.ensurePullRequest(context.Background(), forge, forgejo.Ticket{
		Owner:  "ac",
		Repo:   "demo",
		Kind:   "issue",
		Number: 8,
		Title:  "Fix it",
	}, "forge-ai/ac/demo/issue-8", "release/1.2")
	if err != nil {
		t.Fatalf("ensurePullRequest() error = %v", err)
	}
	if forge.updatePullRequestIndex != 4 {
		t.Fatalf("UpdatePullRequest index = %d, want 4", forge.updatePullRequestIndex)
	}
	if forge.updatePullRequest.Base != "release/1.2" {
		t.Fatalf("UpdatePullRequest base = %q, want release/1.2", forge.updatePullRequest.Base)
	}
	if forge.createPullRequest.Head != "" {
		t.Fatalf("CreatePullRequest called with head %q, want no create", forge.createPullRequest.Head)
	}
}

type stubAgent struct {
	name string
}

func (a *stubAgent) Run(_ context.Context, _, _, _ string) (agent.Result, error) {
	return agent.Result{}, nil
}

type recordingForgejo struct {
	commentBody            string
	reactionCommentID      int64
	reactionContent        string
	reactionErr            error
	reviewComments         []string
	openPullRequest        *forgejo.PullRequest
	createPullRequest      forgejo.CreatePullRequestRequest
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
	return f.openPullRequest, nil
}

func (f *recordingForgejo) CreatePullRequest(_ context.Context, _ string, _ string, request forgejo.CreatePullRequestRequest) (*forgejo.PullRequest, error) {
	f.createPullRequest = request
	return nil, nil
}

func (f *recordingForgejo) UpdatePullRequest(_ context.Context, _ string, _ string, index int, request forgejo.UpdatePullRequestRequest) (*forgejo.PullRequest, error) {
	f.updatePullRequestIndex = index
	f.updatePullRequest = request
	return f.openPullRequest, nil
}
