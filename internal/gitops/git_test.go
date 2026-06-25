package gitops

import "testing"

func TestBranchName(t *testing.T) {
	got := BranchName("forge-ai", "AC Org", "Demo.Repo", "issue", 12)
	want := "forge-ai/ac-org/demo.repo/issue-12"
	if got != want {
		t.Fatalf("BranchName() = %q, want %q", got, want)
	}
}

func TestSlugFallback(t *testing.T) {
	if got := Slug("///"); got != "item" {
		t.Fatalf("Slug() = %q, want item", got)
	}
}
