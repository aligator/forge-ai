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
	"sync"
	"time"

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

type Stream string

const (
	StreamStdout Stream = "stdout"
	StreamStderr Stream = "stderr"
)

type OutputChunk struct {
	Time   time.Time
	Stream Stream
	Chunk  string
}

type OutputWriter interface {
	WriteOutput(OutputChunk) error
}

type RunOptions struct {
	Workdir   string
	Prompt    string
	SessionID string
	Stdin     io.Reader
	Output    OutputWriter
}

func NewRunner(cfg config.AgentConfig, logger *slog.Logger) *Runner {
	return &Runner{cfg: cfg, logger: logger}
}

func (r *Runner) Run(ctx context.Context, workdir, prompt, sessionID string) (Result, error) {
	return r.RunWithOptions(ctx, RunOptions{
		Workdir:   workdir,
		Prompt:    prompt,
		SessionID: sessionID,
	})
}

func (r *Runner) RunWithOptions(ctx context.Context, options RunOptions) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, r.cfg.Timeout)
	defer cancel()

	var cmd *exec.Cmd
	knownSessionID := options.SessionID
	adapter := adapterFor(r.cfg)
	if r.cfg.CommandTemplate != "" {
		cmd = exec.CommandContext(ctx, "sh", "-c", r.cfg.CommandTemplate)
		cmd.Env = effectiveEnv(append(r.cfg.ExtraEnv, "FORGE_AI_PROMPT="+options.Prompt, "FORGE_AI_SESSION_ID="+options.SessionID)...)
	} else {
		invocation := adapter.Invocation(r.cfg.Args, options.Prompt, options.SessionID)
		if invocation.SessionID != "" {
			knownSessionID = invocation.SessionID
		}
		cmd = exec.CommandContext(ctx, r.cfg.Bin, invocation.Args...)
		if len(r.cfg.ExtraEnv) > 0 {
			cmd.Env = effectiveEnv(r.cfg.ExtraEnv...)
		}
	}
	cmd.Dir = options.Workdir
	cmd.Stdin = options.Stdin // nil explicitly closes stdin; subprocesses that read get immediate EOF

	collector := newOutputCollector(12000, adapter, knownSessionID)
	redactor := NewRedactor(effectiveCmdEnv(cmd))
	stdout := newProcessOutputWriter(StreamStdout, os.Stdout, options.Output, collector, redactor.Stream())
	stderr := newProcessOutputWriter(StreamStderr, os.Stderr, options.Output, collector, redactor.Stream())
	cmd.Stdout = stdout
	cmd.Stderr = stderr

	r.logger.Info("starting agent", "workdir", options.Workdir, "command", redactor.Redact(commandLine(cmd)), "session_id", knownSessionID)
	if out, err := exec.CommandContext(ctx, "find", options.Workdir, "-maxdepth", "3", "-not", "-path", "*/.git/*").Output(); err == nil {
		r.logger.Debug("workspace contents", "files", strings.TrimSpace(string(out)))
	}
	err := cmd.Run()
	stdout.Close()
	stderr.Close()
	if err != nil {
		r.logger.Error("agent failed", "error", err)
	} else {
		r.logger.Info("agent finished")
	}
	if writerErr := stdout.Err(); writerErr != nil && err == nil {
		err = writerErr
	}
	if writerErr := stderr.Err(); writerErr != nil && err == nil {
		err = writerErr
	}
	return Result{Output: collector.Tail(), SessionID: collector.SessionID()}, err
}

func effectiveEnv(extra ...string) []string {
	env := os.Environ()
	if len(extra) == 0 {
		return env
	}
	return append(env, extra...)
}

func effectiveCmdEnv(cmd *exec.Cmd) []string {
	if len(cmd.Env) > 0 {
		return cmd.Env
	}
	return os.Environ()
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
	return safeTail(value, limit)
}

// safeTail returns the last byteLimit bytes of s, adjusted forward to the next
// valid UTF-8 rune boundary so multi-byte characters are never split.
func safeTail(s string, byteLimit int) string {
	if byteLimit <= 0 || len(s) <= byteLimit {
		return s
	}
	offset := len(s) - byteLimit
	for offset < len(s) && s[offset]&0xC0 == 0x80 {
		offset++
	}
	return s[offset:]
}

func redact(value string, env []string) string {
	return NewRedactor(env).Redact(value)
}

type outputCollector struct {
	mu        sync.Mutex
	output    bytes.Buffer
	limit     int
	adapter   Adapter
	sessionID string
}

func newOutputCollector(limit int, adapter Adapter, sessionID string) *outputCollector {
	return &outputCollector{limit: limit, adapter: adapter, sessionID: sessionID}
}

func (c *outputCollector) Write(chunk string) {
	if chunk == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.output.WriteString(chunk)
	if c.limit > 0 && c.output.Len() > c.limit {
		value := c.output.String()
		c.output.Reset()
		c.output.WriteString(safeTail(value, c.limit))
	}
	if c.sessionID == "" {
		c.sessionID = c.adapter.ExtractSessionID(c.output.String())
	}
}

func (c *outputCollector) Tail() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return tail(c.output.String(), c.limit)
}

func (c *outputCollector) SessionID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sessionID
}

type processOutputWriter struct {
	stream    Stream
	console   io.Writer
	output    OutputWriter
	collector *outputCollector
	redactor  *StreamRedactor
	mu        sync.Mutex
	err       error
}

func newProcessOutputWriter(stream Stream, console io.Writer, output OutputWriter, collector *outputCollector, redactor *StreamRedactor) *processOutputWriter {
	return &processOutputWriter{
		stream:    stream,
		console:   console,
		output:    output,
		collector: collector,
		redactor:  redactor,
	}
}

func (w *processOutputWriter) Write(p []byte) (int, error) {
	w.writeRedacted(w.redactor.RedactChunk(string(p)))
	return len(p), nil
}

func (w *processOutputWriter) Close() {
	w.writeRedacted(w.redactor.Close())
}

func (w *processOutputWriter) Err() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.err
}

func (w *processOutputWriter) writeRedacted(chunk string) {
	if chunk == "" {
		return
	}
	w.collector.Write(chunk)
	if _, err := io.WriteString(w.console, chunk); err != nil {
		w.setErr(err)
	}
	if w.output != nil {
		if err := w.output.WriteOutput(OutputChunk{Time: time.Now().UTC(), Stream: w.stream, Chunk: chunk}); err != nil {
			w.setErr(err)
		}
	}
}

func (w *processOutputWriter) setErr(err error) {
	if err == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil {
		w.err = err
	}
}

type Redactor struct {
	secrets []string
	keep    int
}

func NewRedactor(env []string) Redactor {
	var secrets []string
	for _, item := range env {
		key, secret, ok := strings.Cut(item, "=")
		// Minimum length of 8 avoids false positives: short values like "yes",
		// "true", or "1" are too common in non-secret env vars to redact safely.
		if !ok || secret == "" || !sensitiveEnvKey(key) || len(secret) < 8 {
			continue
		}
		secrets = append(secrets, secret)
	}
	keep := 0
	for _, secret := range secrets {
		if len(secret)-1 > keep {
			keep = len(secret) - 1
		}
	}
	return Redactor{secrets: secrets, keep: keep}
}

func (r Redactor) Redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	return value
}

func (r Redactor) Stream() *StreamRedactor {
	return &StreamRedactor{redactor: r}
}

type StreamRedactor struct {
	redactor Redactor
	pending  string
}

func (r *StreamRedactor) RedactChunk(chunk string) string {
	if r.redactor.keep == 0 {
		return chunk
	}
	value := r.pending + chunk
	redacted := r.redactor.Redact(value)
	if redacted != value {
		r.pending = ""
		return redacted
	}
	if len(value) <= r.redactor.keep {
		r.pending = value
		return ""
	}
	emitLen := len(value) - r.redactor.keep
	emit := value[:emitLen]
	r.pending = value[emitLen:]
	return r.redactor.Redact(emit)
}

func (r *StreamRedactor) Close() string {
	value := r.pending
	r.pending = ""
	return r.redactor.Redact(value)
}

func sensitiveEnvKey(key string) bool {
	key = strings.ToUpper(key)
	return strings.Contains(key, "TOKEN") || strings.Contains(key, "PASSWORD") || strings.Contains(key, "SECRET") || strings.Contains(key, "KEY")
}
