package gitops

import (
	"context"
	"io"
	"log/slog"
	"os"
	"strings"
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
	workdir, _, err := git.Prepare(ctx, workspaceRoot, cloneURL, "", "acme", "demo", "feature", "main", config.GitIdentity{})
	if err != nil {
		t.Fatalf("Prepare() initial error = %v", err)
	}
	writeTestFile(t, workdir+"/README.md", "local divergent\n")
	runTestGit(t, ctx, workdir, "commit", "-am", "local divergent")
	writeTestFile(t, workdir+"/scratch.txt", "dirty\n")

	writeTestFile(t, seed+"/README.md", "remote v2\n")
	runTestGit(t, ctx, seed, "commit", "-am", "feature v2")
	runTestGit(t, ctx, seed, "push", "origin", "feature")

	workdir, _, err = git.Prepare(ctx, workspaceRoot, cloneURL, "", "acme", "demo", "feature", "main", config.GitIdentity{})
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

func TestPushMergesRemoteBranchBeforePush(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := root + "/remote.git"
	seed := root + "/seed"
	workdir := root + "/work"
	cloneURL := "file://" + remote

	runTestGit(t, ctx, "", "init", "--bare", remote)
	runTestGit(t, ctx, "", "init", seed)
	configureTestGitUser(t, ctx, seed)
	runTestGit(t, ctx, seed, "checkout", "-b", "main")
	writeTestFile(t, seed+"/README.md", "base\n")
	runTestGit(t, ctx, seed, "add", "README.md")
	runTestGit(t, ctx, seed, "commit", "-m", "base")
	runTestGit(t, ctx, seed, "remote", "add", "origin", cloneURL)
	runTestGit(t, ctx, seed, "push", "-u", "origin", "main")

	runTestGit(t, ctx, "", "clone", cloneURL, workdir)
	configureTestGitUser(t, ctx, workdir)
	runTestGit(t, ctx, workdir, "checkout", "-b", "feature", "origin/main")
	writeTestFile(t, workdir+"/agent.txt", "agent\n")
	runTestGit(t, ctx, workdir, "add", "agent.txt")
	runTestGit(t, ctx, workdir, "commit", "-m", "agent work")

	runTestGit(t, ctx, seed, "checkout", "-b", "feature")
	writeTestFile(t, seed+"/remote.txt", "remote\n")
	runTestGit(t, ctx, seed, "add", "remote.txt")
	runTestGit(t, ctx, seed, "commit", "-m", "remote work")
	runTestGit(t, ctx, seed, "push", "-u", "origin", "feature")

	git := New(config.GitConfig{RemoteName: "origin"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := git.Push(ctx, workdir, "feature"); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	out := runTestGit(t, ctx, workdir, "log", "--oneline", "--merges", "-1")
	if !strings.Contains(out, "Merge remote-tracking branch 'origin/feature'") {
		t.Fatalf("merge log = %q, want remote feature merge", out)
	}
	runTestGit(t, ctx, seed, "fetch", "origin", "feature")
	remoteAgent := runTestGit(t, ctx, seed, "show", "origin/feature:agent.txt")
	if remoteAgent != "agent\n" {
		t.Fatalf("remote agent.txt = %q, want agent", remoteAgent)
	}
	remoteFile := runTestGit(t, ctx, seed, "show", "origin/feature:remote.txt")
	if remoteFile != "remote\n" {
		t.Fatalf("remote remote.txt = %q, want remote", remoteFile)
	}
}

func TestPushAllowsNewRemoteBranch(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := root + "/remote.git"
	seed := root + "/seed"
	workdir := root + "/work"
	cloneURL := "file://" + remote

	runTestGit(t, ctx, "", "init", "--bare", remote)
	runTestGit(t, ctx, "", "init", seed)
	configureTestGitUser(t, ctx, seed)
	runTestGit(t, ctx, seed, "checkout", "-b", "main")
	writeTestFile(t, seed+"/README.md", "base\n")
	runTestGit(t, ctx, seed, "add", "README.md")
	runTestGit(t, ctx, seed, "commit", "-m", "base")
	runTestGit(t, ctx, seed, "remote", "add", "origin", cloneURL)
	runTestGit(t, ctx, seed, "push", "-u", "origin", "main")

	runTestGit(t, ctx, "", "clone", cloneURL, workdir)
	configureTestGitUser(t, ctx, workdir)
	runTestGit(t, ctx, workdir, "checkout", "-b", "feature", "origin/main")
	writeTestFile(t, workdir+"/agent.txt", "agent\n")
	runTestGit(t, ctx, workdir, "add", "agent.txt")
	runTestGit(t, ctx, workdir, "commit", "-m", "agent work")

	git := New(config.GitConfig{RemoteName: "origin"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err := git.Push(ctx, workdir, "feature"); err != nil {
		t.Fatalf("Push() error = %v", err)
	}
	runTestGit(t, ctx, seed, "fetch", "origin", "feature")
	remoteAgent := runTestGit(t, ctx, seed, "show", "origin/feature:agent.txt")
	if remoteAgent != "agent\n" {
		t.Fatalf("remote agent.txt = %q, want agent", remoteAgent)
	}
}

func TestPushPreservesCommittedWorkWhenRemoteMergeConflicts(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	remote := root + "/remote.git"
	seed := root + "/seed"
	workdir := root + "/work"
	cloneURL := "file://" + remote

	runTestGit(t, ctx, "", "init", "--bare", remote)
	runTestGit(t, ctx, "", "init", seed)
	configureTestGitUser(t, ctx, seed)
	runTestGit(t, ctx, seed, "checkout", "-b", "main")
	writeTestFile(t, seed+"/README.md", "base\n")
	runTestGit(t, ctx, seed, "add", "README.md")
	runTestGit(t, ctx, seed, "commit", "-m", "base")
	runTestGit(t, ctx, seed, "remote", "add", "origin", cloneURL)
	runTestGit(t, ctx, seed, "push", "-u", "origin", "main")

	runTestGit(t, ctx, seed, "checkout", "-b", "feature")
	writeTestFile(t, seed+"/README.md", "remote v1\n")
	runTestGit(t, ctx, seed, "commit", "-am", "remote v1")
	runTestGit(t, ctx, seed, "push", "-u", "origin", "feature")

	runTestGit(t, ctx, "", "clone", cloneURL, workdir)
	configureTestGitUser(t, ctx, workdir)
	runTestGit(t, ctx, workdir, "checkout", "feature")
	writeTestFile(t, workdir+"/README.md", "agent work\n")
	runTestGit(t, ctx, workdir, "commit", "-am", "agent work")

	writeTestFile(t, seed+"/README.md", "remote v2\n")
	runTestGit(t, ctx, seed, "commit", "-am", "remote v2")
	runTestGit(t, ctx, seed, "push", "origin", "feature")

	git := New(config.GitConfig{RemoteName: "origin"}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	err := git.Push(ctx, workdir, "feature")
	if err == nil {
		t.Fatal("Push() error = nil, want conflict error")
	}
	if !strings.Contains(err.Error(), "committed work preserved on feature-forge-ai-recovery-") {
		t.Fatalf("Push() error = %v, want recovery branch message", err)
	}
	status := runTestGit(t, ctx, workdir, "status", "--porcelain")
	if status != "" {
		t.Fatalf("status = %q, want clean after merge abort", status)
	}

	out := runTestGit(t, ctx, seed, "ls-remote", "--heads", "origin")
	recoveryBranch := ""
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "refs/heads/feature-forge-ai-recovery-") {
			recoveryBranch = strings.TrimPrefix(line[strings.LastIndex(line, "refs/heads/"):], "refs/heads/")
			break
		}
	}
	if recoveryBranch == "" {
		t.Fatalf("ls-remote output missing recovery branch:\n%s", out)
	}
	runTestGit(t, ctx, seed, "fetch", "origin", recoveryBranch)
	got := runTestGit(t, ctx, seed, "show", "origin/"+recoveryBranch+":README.md")
	if got != "agent work\n" {
		t.Fatalf("recovery README = %q, want agent work", got)
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

func configureTestGitUser(t *testing.T, ctx context.Context, dir string) {
	t.Helper()
	runTestGit(t, ctx, dir, "config", "user.name", "Test User")
	runTestGit(t, ctx, dir, "config", "user.email", "test@example.invalid")
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
