package gitops

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"codeberg.org/forge-ai/internal/config"
)

type Git struct {
	cfg    config.GitConfig
	logger *slog.Logger
}

func New(cfg config.GitConfig, logger *slog.Logger) *Git {
	return &Git{cfg: cfg, logger: logger}
}

func (g *Git) Prepare(ctx context.Context, workspaceRoot, cloneURL, token, owner, repo, branch, baseBranch string) (string, error) {
	if cloneURL == "" {
		return "", fmt.Errorf("missing clone url")
	}

	workdir := filepath.Join(workspaceRoot, Slug(owner+"-"+repo+"-"+branch))
	if _, err := os.Stat(filepath.Join(workdir, ".git")); os.IsNotExist(err) {
		if err := os.MkdirAll(workspaceRoot, 0o755); err != nil {
			return "", err
		}
		if _, err := run(ctx, "", "git", "clone", withToken(cloneURL, token), workdir); err != nil {
			return "", err
		}
	}

	if dirty, err := g.IsDirty(ctx, workdir); err != nil {
		return "", err
	} else if dirty {
		return "", fmt.Errorf("workspace %s has uncommitted changes", workdir)
	}

	if _, err := run(ctx, workdir, "git", "config", "user.name", g.cfg.UserName); err != nil {
		return "", err
	}
	if _, err := run(ctx, workdir, "git", "config", "user.email", g.cfg.UserEmail); err != nil {
		return "", err
	}
	if _, err := run(ctx, workdir, "git", "remote", "set-url", g.cfg.RemoteName, withToken(cloneURL, token)); err != nil {
		return "", err
	}
	if _, err := run(ctx, workdir, "git", "fetch", "--prune", g.cfg.RemoteName); err != nil {
		return "", err
	}
	baseBranch = g.resolveBaseBranch(ctx, workdir, baseBranch)

	remoteBranchExists := g.RemoteBranchExists(ctx, workdir, branch)
	if remoteBranchExists {
		if _, err := run(ctx, workdir, "git", "checkout", branch); err != nil {
			if _, err := run(ctx, workdir, "git", "checkout", "-b", branch, g.cfg.RemoteName+"/"+branch); err != nil {
				return "", err
			}
		}
		if _, err := run(ctx, workdir, "git", "pull", "--ff-only", g.cfg.RemoteName, branch); err != nil {
			return "", err
		}
		return workdir, nil
	}

	if _, err := run(ctx, workdir, "git", "checkout", "-B", baseBranch, g.cfg.RemoteName+"/"+baseBranch); err != nil {
		return "", err
	}
	if _, err := run(ctx, workdir, "git", "pull", "--ff-only", g.cfg.RemoteName, baseBranch); err != nil {
		return "", err
	}
	if _, err := run(ctx, workdir, "git", "checkout", "-B", branch); err != nil {
		return "", err
	}

	return workdir, nil
}

func (g *Git) IsDirty(ctx context.Context, workdir string) (bool, error) {
	out, err := run(ctx, workdir, "git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

func (g *Git) CommitIfDirty(ctx context.Context, workdir, message string) (bool, error) {
	dirty, err := g.IsDirty(ctx, workdir)
	if err != nil {
		return false, err
	}
	if !dirty {
		return false, nil
	}
	if _, err := run(ctx, workdir, "git", "add", "-A"); err != nil {
		return false, err
	}
	if _, err := run(ctx, workdir, "git", "commit", "-m", message); err != nil {
		return false, err
	}
	return true, nil
}

func (g *Git) Push(ctx context.Context, workdir, branch string) error {
	_, err := run(ctx, workdir, "git", "push", "-u", g.cfg.RemoteName, branch)
	return err
}

func (g *Git) RemoteBranchExists(ctx context.Context, workdir, branch string) bool {
	out, err := run(ctx, workdir, "git", "ls-remote", "--heads", g.cfg.RemoteName, branch)
	return err == nil && strings.TrimSpace(out) != ""
}

func (g *Git) resolveBaseBranch(ctx context.Context, workdir, preferred string) string {
	if preferred != "" && g.RemoteBranchExists(ctx, workdir, preferred) {
		return preferred
	}

	if _, err := run(ctx, workdir, "git", "remote", "set-head", g.cfg.RemoteName, "--auto"); err == nil {
		out, err := run(ctx, workdir, "git", "symbolic-ref", "--short", "refs/remotes/"+g.cfg.RemoteName+"/HEAD")
		if err == nil {
			branch := strings.TrimPrefix(strings.TrimSpace(out), g.cfg.RemoteName+"/")
			if branch != "" {
				return branch
			}
		}
	}

	out, err := run(ctx, workdir, "git", "ls-remote", "--heads", g.cfg.RemoteName)
	if err == nil {
		for _, line := range strings.Split(out, "\n") {
			if _, ref, ok := strings.Cut(line, "refs/heads/"); ok && strings.TrimSpace(ref) != "" {
				return strings.TrimSpace(ref)
			}
		}
	}

	return preferred
}

func run(ctx context.Context, dir, name string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	var output bytes.Buffer
	cmd.Stdout = &output
	cmd.Stderr = &output
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s %s: %w: %s", name, strings.Join(args, " "), err, strings.TrimSpace(output.String()))
	}
	return output.String(), nil
}

func BranchName(prefix, owner, repo, kind string, number int) string {
	return strings.Join([]string{
		Slug(prefix),
		Slug(owner),
		Slug(repo),
		fmt.Sprintf("%s-%d", Slug(kind), number),
	}, "/")
}

func Slug(value string) string {
	value = strings.ToLower(value)
	value = invalidBranchChars.ReplaceAllString(value, "-")
	value = strings.Trim(value, "-./")
	for strings.Contains(value, "--") {
		value = strings.ReplaceAll(value, "--", "-")
	}
	if value == "" {
		return "item"
	}
	return value
}

var invalidBranchChars = regexp.MustCompile(`[^a-z0-9._-]+`)

func withToken(rawURL, token string) string {
	if token == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return rawURL
	}
	if parsed.User != nil {
		return rawURL
	}
	parsed.User = url.UserPassword("token", token)
	return parsed.String()
}
