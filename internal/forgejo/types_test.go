package forgejo

import "testing"

func TestTicketFromIssuePayload(t *testing.T) {
	payload := WebhookPayload{
		Repository: Repository{
			Name:          "demo",
			CloneURL:      "https://forgejo.local/ac/demo.git",
			DefaultBranch: "main",
			Owner:         User{Login: "ac"},
		},
		Issue: &Issue{
			Index:  42,
			Title:  "Fix it",
			Labels: []Label{{Name: "ai"}},
		},
		Comment: &Comment{ID: 123},
	}

	ticket, ok := TicketFromPayload("issues", payload)
	if !ok {
		t.Fatal("expected ticket")
	}
	if ticket.Owner != "ac" || ticket.Repo != "demo" || ticket.Kind != "issue" || ticket.Number != 42 {
		t.Fatalf("unexpected ticket: %#v", ticket)
	}
	if ticket.CommentID != 123 {
		t.Fatalf("CommentID = %d, want 123", ticket.CommentID)
	}
}

func TestTicketFromPullRequestPayload(t *testing.T) {
	payload := WebhookPayload{
		Repository: Repository{
			Name:          "demo",
			DefaultBranch: "main",
			Owner:         User{UserName: "ac"},
		},
		Pull: &PullRequest{
			Index: 7,
			Title: "Improve it",
			Head: PullRequestBranch{
				Ref:  "feature",
				Repo: Repository{CloneURL: "https://forgejo.local/ac/demo-fork.git"},
			},
			Base: PullRequestBranch{Ref: "main"},
		},
	}

	ticket, ok := TicketFromPayload("pull_request", payload)
	if !ok {
		t.Fatal("expected ticket")
	}
	if ticket.Kind != "pr" || ticket.Number != 7 || ticket.HeadBranch != "feature" || ticket.BaseBranch != "main" {
		t.Fatalf("unexpected ticket: %#v", ticket)
	}
	if ticket.CloneURL != "https://forgejo.local/ac/demo-fork.git" {
		t.Fatalf("CloneURL = %q", ticket.CloneURL)
	}
}

func TestTicketFromPullRequestReviewPayloadUsesReviewBody(t *testing.T) {
	payload := WebhookPayload{
		Action: "reviewed",
		Repository: Repository{
			Name:          "demo",
			DefaultBranch: "main",
			Owner:         User{Login: "ac"},
		},
		Pull: &PullRequest{
			Index: 7,
			Title: "Improve it",
			Head: PullRequestBranch{
				Ref:  "feature",
				Repo: Repository{CloneURL: "https://forgejo.local/ac/demo.git"},
			},
			Base: PullRequestBranch{Ref: "main"},
		},
		Review: &Review{
			ID:   99,
			Body: "@forge-ai say hello from review",
		},
	}

	ticket, ok := TicketFromPayload("pull_request_comment", payload)
	if !ok {
		t.Fatal("expected ticket")
	}
	if ticket.Instruction != "@forge-ai say hello from review" {
		t.Fatalf("Instruction = %q", ticket.Instruction)
	}
	if ticket.CommentID != 0 {
		t.Fatalf("CommentID = %d, want 0 for review payload", ticket.CommentID)
	}
}
