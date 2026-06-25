package service

import (
	"context"
	"errors"
	"testing"

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

func TestShouldRunOnlyForMention(t *testing.T) {
	svc := New(Options{
		Config: config.Config{
			TriggerMention:       "@forge-ai",
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

func TestPostStartAckReactsToComment(t *testing.T) {
	forge := &recordingForgejo{}
	svc := New(Options{
		Config:  config.Config{MaxConcurrent: 1},
		Forgejo: forge,
	})

	err := svc.postStartAck(context.Background(), forgejo.Ticket{
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

	err := svc.postStartAck(context.Background(), forgejo.Ticket{
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

	err := svc.postStartAck(context.Background(), forgejo.Ticket{
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

	err := svc.postSuccess(context.Background(), forgejo.Ticket{
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

type recordingForgejo struct {
	commentBody       string
	reactionCommentID int64
	reactionContent   string
	reactionErr       error
	reviewComments    []string
}

func (f *recordingForgejo) GetPullReviewComments(_ context.Context, _, _ string, _ int, _ int64) ([]string, error) {
	return f.reviewComments, nil
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
	return nil, nil
}

func (f *recordingForgejo) CreatePullRequest(context.Context, string, string, forgejo.CreatePullRequestRequest) (*forgejo.PullRequest, error) {
	return nil, nil
}
