package service

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeberg.org/forge-ai/internal/agent"
	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/forgejo"
	"codeberg.org/forge-ai/internal/runstore"
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

func TestBranchForPullRequestStripsHeadsRef(t *testing.T) {
	got := branchForTicket(config.Config{BranchPrefix: "forge-ai"}, forgejo.Ticket{
		Owner:      "ac",
		Repo:       "demo",
		Kind:       "pr",
		Number:     8,
		HeadBranch: "refs/heads/feature/dashboard",
	})
	want := "feature/dashboard"
	if got != want {
		t.Fatalf("branchForTicket() = %q, want %q", got, want)
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

func TestSuccessCommentUsesFinalMessageTemplate(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".forge-ai"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	template := strings.Join([]string{
		"Done on `{{.Branch}}` for {{.RepoFullName}} {{.TicketKind}} #{{.TicketNumber}}.",
		"Committed: {{.Committed}}",
		"Agent: {{.AgentMention}} {{.AgentSessionID}}",
		"Run: {{.RunID}} {{.CreatedBy}}",
		"Branch URL: {{.BranchURL}}",
		"PR: #{{.PullRequest.Number}} {{.PullRequest.URL}}",
	}, "\n")
	if err := os.WriteFile(filepath.Join(workdir, ".forge-ai", "final-message.md"), []byte(template), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runner := &workflowRunner{cfg: config.Config{ForgejoURL: "https://forgejo.example"}}
	state := workflowState{
		ticket: forgejo.Ticket{
			Owner:   "ac",
			Repo:    "demo",
			Kind:    "issue",
			Number:  42,
			HTMLURL: "https://forgejo.example/ac/demo/issues/42",
		},
		run: runstore.Run{
			ID:           "run-42",
			AgentMention: "@codex",
			AgentType:    "codex",
			CreatedBy:    "aligator",
		},
		branch:    "forge-ai/ac/demo/issue-42",
		base:      "main",
		workdir:   workdir,
		result:    agent.Result{SessionID: "session-42"},
		committed: true,
	}
	pull := &forgejo.PullRequest{Index: 7, HTMLURL: "https://forgejo.example/ac/demo/pulls/7"}

	got, templated, err := runner.successCommentForState(state, pull)
	if err != nil {
		t.Fatalf("successCommentForState() error = %v", err)
	}
	if !templated {
		t.Fatalf("successCommentForState() templated = false, want true")
	}
	for _, want := range []string{
		"Done on `forge-ai/ac/demo/issue-42` for ac/demo issue #42.",
		"Committed: true",
		"Agent: @codex session-42",
		"Run: run-42 aligator",
		"Branch URL: https://forgejo.example/ac/demo/src/branch/forge-ai%2Fac%2Fdemo%2Fissue-42",
		"PR: #7 https://forgejo.example/ac/demo/pulls/7",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("successCommentForState() = %q, want %q", got, want)
		}
	}
}

func TestSuccessCommentFallsBackWithoutFinalMessageTemplate(t *testing.T) {
	runner := &workflowRunner{}
	state := workflowState{
		branch:    "work",
		workdir:   t.TempDir(),
		result:    agent.Result{SessionID: "session-123"},
		committed: true,
	}

	got, templated, err := runner.successCommentForState(state, nil)
	if err != nil {
		t.Fatalf("successCommentForState() error = %v", err)
	}
	if templated {
		t.Fatalf("successCommentForState() templated = true, want false")
	}
	if !strings.Contains(got, "forge-ai completed work on `work`.") || !strings.Contains(got, "Agent session: `session-123`") {
		t.Fatalf("successCommentForState() = %q, want fallback comment", got)
	}
}

func TestSuccessCommentReturnsTemplateError(t *testing.T) {
	workdir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workdir, ".forge-ai"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(workdir, ".forge-ai", "final-message.md"), []byte("{{.Missing}}"), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	runner := &workflowRunner{}

	_, _, err := runner.successCommentForState(workflowState{workdir: workdir}, nil)
	if err == nil || !strings.Contains(err.Error(), "render final message template") {
		t.Fatalf("successCommentForState() error = %v, want render error", err)
	}
}

func TestPostFinalSuccessPostsTemplateCommentForTriggerComment(t *testing.T) {
	forge := &recordingForgejo{}
	err := postFinalSuccess(context.Background(), forge, forgejo.Ticket{
		Owner:     "ac",
		Repo:      "demo",
		Number:    1,
		CommentID: 42,
	}, "custom final", true)
	if err != nil {
		t.Fatalf("postFinalSuccess() error = %v", err)
	}
	if forge.commentBody != "custom final" {
		t.Fatalf("commentBody = %q, want custom final", forge.commentBody)
	}
	if forge.reactionContent != "" {
		t.Fatalf("reactionContent = %q, want no reaction", forge.reactionContent)
	}
}

func TestRunAgentPersistsStreamingLogChunks(t *testing.T) {
	store := &recordingRunStore{}
	runner := &workflowRunner{runStore: store}
	agent := &streamingStubAgent{
		chunks: []agent.OutputChunk{
			{Stream: agent.StreamStdout, Chunk: "out\n"},
			{Stream: agent.StreamStderr, Chunk: "err\n"},
		},
		result: agent.Result{Output: "out\nerr", SessionID: "session-123"},
	}

	result, err := runner.runAgent(context.Background(), agent, t.TempDir(), "prompt", "", "run-1")
	if err != nil {
		t.Fatalf("runAgent() error = %v", err)
	}
	if result.SessionID != "session-123" {
		t.Fatalf("SessionID = %q, want session-123", result.SessionID)
	}
	if len(store.logs) != 2 {
		t.Fatalf("logs = %+v, want two chunks", store.logs)
	}
	if store.logs[0].Stream != "stdout" || store.logs[0].Chunk != "out\n" || store.logs[1].Stream != "stderr" || store.logs[1].Chunk != "err\n" {
		t.Fatalf("logs = %+v, want stdout/stderr chunks", store.logs)
	}
	if len(store.runs) > 0 && store.runs[0].SessionID != "session-123" {
		t.Fatalf("stored session = %q, want session-123", store.runs[0].SessionID)
	}
}

func TestAgentPromptDefaultsUseStoredAgentSettings(t *testing.T) {
	store := &recordingRunStore{
		settings: map[string]runstore.AgentSettings{
			"@codex": {
				Mention:     "@codex",
				Enabled:     true,
				ToolHints:   "- stored hint",
				AllowGit:    true,
				AllowGitSet: true,
			},
		},
	}
	runner := &workflowRunner{
		cfg: config.Config{
			AgentAllowGit:  false,
			AgentToolHints: "- env hint",
			Agents: []config.AgentRoute{{
				Mention: "@codex",
				Agent: config.AgentConfig{
					ToolHints: "- route hint",
				},
			}},
		},
		runStore: store,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	allowGit, toolHints := runner.agentPromptDefaults("@codex")
	if !allowGit {
		t.Fatal("allowGit = false, want stored true")
	}
	if toolHints != "- stored hint" {
		t.Fatalf("toolHints = %q, want stored value", toolHints)
	}
}

func TestRunPreservesChangesWhenAgentFails(t *testing.T) {
	store := &recordingRunStore{
		runs: []runstore.Run{{ID: "run-1"}},
	}
	git := &recordingGit{workdir: t.TempDir()}
	forge := &recordingForgejo{}
	runner := &workflowRunner{
		cfg:      config.Config{WorkspaceDir: t.TempDir(), BranchPrefix: "forge-ai"},
		git:      git,
		runStore: store,
		logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	ticket := forgejo.Ticket{
		Owner:         "ac",
		Repo:          "demo",
		Kind:          "issue",
		Number:        42,
		Title:         "aborted",
		CloneURL:      "https://forgejo.example/ac/demo.git",
		DefaultBranch: "main",
	}
	run := runstore.Run{
		ID:           "run-1",
		Branch:       "forge-ai/ac/demo/issue-42",
		BaseBranch:   "main",
		AgentMention: "@codex",
	}

	err := runner.run(context.Background(), forge, ticket, &stubAgent{
		result: agent.Result{Output: "partial work", SessionID: "session-42"},
		err:    errors.New("agent timed out"),
	}, run, config.GitIdentity{})
	if err == nil || !strings.Contains(err.Error(), "agent failed") {
		t.Fatalf("run() error = %v, want agent failed", err)
	}
	if git.commitCalls != 1 || git.pushCalls != 1 {
		t.Fatalf("git calls commit=%d push=%d, want 1/1", git.commitCalls, git.pushCalls)
	}
	if git.pushBranch != "forge-ai/ac/demo/issue-42" {
		t.Fatalf("push branch = %q, want issue branch", git.pushBranch)
	}
	if git.commitMessage != "forge-ai: work on issue #42" {
		t.Fatalf("commit message = %q", git.commitMessage)
	}
	storedRun, _ := store.GetRun(context.Background(), "run-1")
	if storedRun.Status != runstore.StatusFailed {
		t.Fatalf("run status = %q, want failed", storedRun.Status)
	}
	if !store.hasEvent("commit_checked") || !store.hasEvent("pushed") || !store.hasEvent("abort_preserved") {
		t.Fatalf("events = %+v", store.events)
	}
	if !strings.Contains(forge.commentBody, "partial work") || !strings.Contains(forge.commentBody, "Changes were committed if needed and pushed") {
		t.Fatalf("commentBody = %q, want output and preserved branch note", forge.commentBody)
	}
}

func TestPostStartAckReactsToComment(t *testing.T) {
	forge := &recordingForgejo{}
	err := postStartAckWith(context.Background(), forge, forgejo.Ticket{
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
	err := postStartAckWith(context.Background(), forge, forgejo.Ticket{
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
	err := postStartAckWith(context.Background(), forge, forgejo.Ticket{
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
	err := postSuccess(context.Background(), forge, forgejo.Ticket{
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
	err := postSuccess(context.Background(), forge, forgejo.Ticket{
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
	_, err := ensurePullRequest(context.Background(), forge, forgejo.Ticket{
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

func TestEnsurePullRequestStripsBaseBranchRef(t *testing.T) {
	forge := &recordingForgejo{}
	_, err := ensurePullRequest(context.Background(), forge, forgejo.Ticket{
		Owner:  "ac",
		Repo:   "demo",
		Kind:   "issue",
		Number: 8,
		Title:  "Fix it",
	}, "forge-ai/ac/demo/issue-8", "refs/heads/feature/dashboard")
	if err != nil {
		t.Fatalf("ensurePullRequest() error = %v", err)
	}
	if forge.createPullRequest.Base != "feature/dashboard" {
		t.Fatalf("CreatePullRequest base = %q, want feature/dashboard", forge.createPullRequest.Base)
	}
}

func TestEnsurePullRequestRetargetsExistingPullRequest(t *testing.T) {
	forge := &recordingForgejo{
		openPullRequest: &forgejo.PullRequest{
			Index: 4,
			Base:  forgejo.PullRequestBranch{Ref: "main"},
		},
	}
	_, err := ensurePullRequest(context.Background(), forge, forgejo.Ticket{
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

func TestEnsurePullRequestReturnsExistingAfterCreateFailure(t *testing.T) {
	forge := &recordingForgejo{
		createPullRequestErr: errors.New("500 internal server error"),
		openPullRequests: []*forgejo.PullRequest{
			nil,
			{Index: 9, HTMLURL: "https://forgejo.example/ac/demo/pulls/9"},
		},
	}
	pull, err := ensurePullRequest(context.Background(), forge, forgejo.Ticket{
		Owner:  "ac",
		Repo:   "demo",
		Kind:   "issue",
		Number: 8,
		Title:  "Fix it",
	}, "forge-ai/ac/demo/issue-8", "main")
	if err != nil {
		t.Fatalf("ensurePullRequest() error = %v", err)
	}
	if pull == nil || pull.NumberValue() != 9 {
		t.Fatalf("ensurePullRequest() = %#v, want PR #9", pull)
	}
}
