package agent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"

	"codeberg.org/forge-ai/internal/config"
)

type Runner struct {
	cfg    config.AgentConfig
	logger *slog.Logger
}

type Result struct {
	Output    string
	SessionID string
}

func NewRunner(cfg config.AgentConfig, logger *slog.Logger) *Runner {
	return &Runner{cfg: cfg, logger: logger}
}

func (r *Runner) Run(ctx context.Context, workdir, prompt, sessionID string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	knownSessionID := sessionID
	adapter := adapterFor(r.cfg)
	if r.cfg.CommandTemplate != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", r.cfg.CommandTemplate)
		cmd.Env = append(os.Environ(), append(r.cfg.ExtraEnv, "FORGE_AI_PROMPT="+prompt, "FORGE_AI_SESSION_ID="+sessionID)...)
	} else {
		invocation := adapter.Invocation(r.cfg.Args, prompt, sessionID)
		if invocation.SessionID != "" {
			knownSessionID = invocation.SessionID
		}
		cmd = exec.CommandContext(ctx, r.cfg.Bin, invocation.Args...)
		if len(r.cfg.ExtraEnv) > 0 {
			cmd.Env = append(os.Environ(), r.cfg.ExtraEnv...)
		}
	}
	cmd.Dir = workdir
	cmd.Stdin = nil // explicitly closed; subprocesses that read stdin get immediate EOF

	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, os.Stdout)
	cmd.Stderr = io.MultiWriter(&output, os.Stderr)

	r.logger.Info("starting agent", "workdir", workdir, "command", commandLine(cmd), "session_id", knownSessionID)
	if out, err := exec.CommandContext(ctx, "find", workdir, "-maxdepth", "3", "-not", "-path", "*/.git/*").Output(); err == nil {
		r.logger.Debug("workspace contents", "files", strings.TrimSpace(string(out)))
	}
	err := cmd.Run()
	if err != nil {
		r.logger.Error("agent failed", "error", err)
	} else {
		r.logger.Info("agent finished")
	}
	redacted := redact(output.String(), cmd.Env)
	if knownSessionID == "" {
		knownSessionID = adapter.ExtractSessionID(redacted)
	}
	return Result{Output: tail(redacted, 12000), SessionID: knownSessionID}, err
}

func appendExecSubcommand(args []string, subcommand, sessionID string) []string {
	if len(args) == 0 {
		return []string{"exec", subcommand, sessionID}
	}
	for i, arg := range args {
		if arg == "exec" || arg == "e" {
			out := append([]string{}, args[:i+1]...)
			out = append(out, subcommand, sessionID)
			out = append(out, args[i+1:]...)
			return out
		}
	}
	return append(args, subcommand, sessionID)
}

func ensureFlag(args []string, flag string) []string {
	if hasFlag(args, flag) {
		return args
	}
	return append(args, flag)
}

func ensureFlagValue(args []string, flag, value string) []string {
	for i, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			if arg == flag && i+1 < len(args) {
				return args
			}
			return args
		}
	}
	return append(args, flag, value)
}

func hasFlag(args []string, flag string) bool {
	for _, arg := range args {
		if arg == flag || strings.HasPrefix(arg, flag+"=") {
			return true
		}
	}
	return false
}

func commandLine(cmd *exec.Cmd) string {
	args := make([]string, 0, len(cmd.Args)-1)
	for i, arg := range cmd.Args[1:] {
		if len(arg) > 300 || strings.Contains(arg, "\n") {
			args = append(args, fmt.Sprintf("<arg%d:%d bytes>", i+1, len(arg)))
			continue
		}
		args = append(args, arg)
	}
	return fmt.Sprintf("%s %s", cmd.Path, strings.Join(args, " "))
}

func tail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}

func redact(value string, env []string) string {
	for _, item := range env {
		key, secret, ok := strings.Cut(item, "=")
		if !ok || secret == "" || !sensitiveEnvKey(key) || len(secret) < 8 {
			continue
		}
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
}

func sensitiveEnvKey(key string) bool {
	key = strings.ToUpper(key)
	return strings.Contains(key, "TOKEN") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "SECRET") || strings.Contains(key, "KEY")
}
