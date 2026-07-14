package gitops

import (
	"context"
	"io"
	"log/slog"
	"os"
	"testing"

	"codeberg.org/forge-ai/internal/config"
)

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

func TestBranchRefNameStripsHeadsRef(t *testing.T) {
	got := BranchRefName("refs/heads/feature/dashboard")
	want := "feature/dashboard"
	if got != want {
		t.Fatalf("BranchRefName() = %q, want %q", got, want)
	}
}

func TestBranchRefNameStripsRemotePrefix(t *testing.T) {
	got := BranchRefName("origin/feature/dashboard")
	want := "feature/dashboard"
	if got != want {
		t.Fatalf("BranchRefName() = %q, want %q", got, want)
	}
}

func TestPrepareForceSyncsExistingRemoteBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := root + "/remote.git"
	seed := root + "/seed"
	workspaceRoot := root + "/workspaces"
	cloneURL := "file://" + remote

	runTestGit(t, ctx, "", "init", "--bare", remote)
	runTestGit(t, ctx, "", "init", seed)
	runTestGit(t, ctx, seed, "config", "user.name", "Test User")
	runTestGit(t, ctx, seed, "config", "user.email", "test@example.invalid")
	runTestGit(t, ctx, seed, "checkout", "-b", "main")
	writeTestFile(t, seed+"/README.md", "base\n")
	runTestGit(t, ctx, seed, "add", "README.md")
	runTestGit(t, ctx, seed, "commit", "-m", "base")
	runTestGit(t, ctx, seed, "remote", "add", "origin", cloneURL)
	runTestGit(t, ctx, seed, "push", "-u", "origin", "main")
	runTestGit(t, ctx, seed, "checkout", "-b", "feature")
	writeTestFile(t, seed+"/README.md", "remote v1\n")
	runTestGit(t, ctx, seed, "commit", "-am", "feature v1")
	runTestGit(t, ctx, seed, "push", "-u", "origin", "feature")

	git := New(config.GitConfig{RemoteName: "origin", GitIdentity: config.GitIdentity{UserName: "Forge AI", UserEmail: "forge-ai@example.invalid"}}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	workdir, err := git.Prepare(ctx, workspaceRoot, cloneURL, "", "acme", "demo", "feature", "main", config.GitIdentity{})
	if err != nil {
		t.Fatalf("Prepare() initial error = %v", err)
	}
	writeTestFile(t, workdir+"/README.md", "local divergent\n")
	runTestGit(t, ctx, workdir, "commit", "-am", "local divergent")
	writeTestFile(t, workdir+"/scratch.txt", "dirty\n")

	writeTestFile(t, seed+"/README.md", "remote v2\n")
	runTestGit(t, ctx, seed, "commit", "-am", "feature v2")
	runTestGit(t, ctx, seed, "push", "origin", "feature")

	workdir, err = git.Prepare(ctx, workspaceRoot, cloneURL, "", "acme", "demo", "feature", "main", config.GitIdentity{})
	if err != nil {
		t.Fatalf("Prepare() resync error = %v", err)
	}
	got := readTestFile(t, workdir+"/README.md")
	if got != "remote v2\n" {
		t.Fatalf("README = %q, want remote v2", got)
	}
	if _, err := os.Stat(workdir + "/scratch.txt"); !os.IsNotExist(err) {
		t.Fatalf("scratch.txt stat error = %v, want not exist", err)
	}
}

func runTestGit(t *testing.T, ctx context.Context, dir, name string, args ...string) string {
	t.Helper()
	out, err := run(ctx, dir, "git", append([]string{name}, args...)...)
	if err != nil {
		t.Fatalf("git %s %v: %v", name, args, err)
	}
	return out
}

func writeTestFile(t *testing.T, path, contents string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func readTestFile(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(contents)
}
