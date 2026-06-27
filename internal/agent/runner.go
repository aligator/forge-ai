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
	Output string
}

func NewRunner(cfg config.AgentConfig, logger *slog.Logger) *Runner {
	return &Runner{cfg: cfg, logger: logger}
}

func (r *Runner) Run(ctx context.Context, workdir, prompt string) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	if r.cfg.CommandTemplate != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", r.cfg.CommandTemplate)
		cmd.Env = append(os.Environ(), append(r.cfg.ExtraEnv, "FORGE_AI_PROMPT="+prompt)...)
	} else {
		args := append([]string{}, r.cfg.Args...)
		args = append(args, prompt)
		cmd = exec.CommandContext(ctx, r.cfg.Bin, args...)
		if len(r.cfg.ExtraEnv) > 0 {
			cmd.Env = append(os.Environ(), r.cfg.ExtraEnv...)
		}
	}
	cmd.Dir = workdir
	cmd.Stdin = nil // explicitly closed; subprocesses that read stdin get immediate EOF

	var output bytes.Buffer
	cmd.Stdout = io.MultiWriter(&output, os.Stdout)
	cmd.Stderr = io.MultiWriter(&output, os.Stderr)

	r.logger.Info("starting agent", "workdir", workdir, "command", commandLine(cmd))
	if out, err := exec.CommandContext(ctx, "find", workdir, "-maxdepth", "3", "-not", "-path", "*/.git/*").Output(); err == nil {
		r.logger.Debug("workspace contents", "files", strings.TrimSpace(string(out)))
	}
	err := cmd.Run()
	if err != nil {
		r.logger.Error("agent failed", "error", err)
	} else {
		r.logger.Info("agent finished")
	}
	return Result{Output: tail(output.String(), 12000)}, err
}

func commandLine(cmd *exec.Cmd) string {
	return fmt.Sprintf("%s %s", cmd.Path, strings.Join(cmd.Args[1:], " "))
}

func tail(value string, limit int) string {
	value = strings.TrimSpace(value)
	if len(value) <= limit {
		return value
	}
	return value[len(value)-limit:]
}
