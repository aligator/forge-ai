package agent

import (
	"context"
	"io"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"codeberg.org/forge-ai/internal/config"
)

func TestRedactSensitiveEnvValues(t *testing.T) {
	got := redact("token=abc123456789 password=secret12345 ok=value", []string{
		"FORGEJO_ACCESS_TOKEN=abc123456789",
		"FORGEJO_BOOTSTRAP_PASSWORD=secret12345",
		"FORGEJO_URL=http://forgejo:3000",
	})
	want := "token=<redacted> password=<redacted> ok=value"
	if got != want {
		t.Fatalf("redact() = %q, want %q", got, want)
	}
}

func TestStreamRedactorRedactsSecretSplitAcrossChunks(t *testing.T) {
	redactor := NewRedactor([]string{"API_TOKEN=abc123456789"}).Stream()
	got := redactor.RedactChunk("token=abc123") + redactor.RedactChunk("456789\n") + redactor.Close()
	want := "token=<redacted>\n"
	if got != want {
		t.Fatalf("stream redaction = %q, want %q", got, want)
	}
}

func TestRunnerStreamsRedactedStdoutAndStderr(t *testing.T) {
	output := &recordingOutputWriter{}
	runner := NewRunner(config.AgentConfig{
		CommandTemplate: `printf '{"thread_id":"session-abc123"}\n'; printf 'out:%s\n' "$SECRET_TOKEN"; printf 'err:%s\n' "$SECRET_TOKEN" >&2`,
		ExtraEnv:        []string{"SECRET_TOKEN=abc123456789"},
		Timeout:         5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := runner.RunWithOptions(context.Background(), RunOptions{
		Workdir: t.TempDir(),
		Output:  output,
	})
	if err != nil {
		t.Fatalf("RunWithOptions() error = %v", err)
	}
	if result.SessionID != "session-abc123" {
		t.Fatalf("SessionID = %q, want session-abc123", result.SessionID)
	}
	if strings.Contains(result.Output, "abc123456789") {
		t.Fatalf("result output contains secret: %q", result.Output)
	}
	if !strings.Contains(result.Output, "<redacted>") {
		t.Fatalf("result output missing redaction: %q", result.Output)
	}
	if !output.has(StreamStdout, "out:<redacted>") {
		t.Fatalf("stdout chunks = %+v, want redacted stdout", output.chunks)
	}
	if !output.has(StreamStderr, "err:<redacted>") {
		t.Fatalf("stderr chunks = %+v, want redacted stderr", output.chunks)
	}
	if output.contains("abc123456789") {
		t.Fatalf("streamed chunks contain secret: %+v", output.chunks)
	}
}

func TestRunnerRedactsInheritedEnvForNormalCommand(t *testing.T) {
	t.Setenv("AGENT_SECRET_TOKEN", "abc123456789")
	output := &recordingOutputWriter{}
	runner := NewRunner(config.AgentConfig{
		Bin:     "sh",
		Args:    []string{"-c", `printf 'out:%s\n' "$AGENT_SECRET_TOKEN"`},
		Timeout: 5 * time.Second,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	result, err := runner.RunWithOptions(context.Background(), RunOptions{
		Workdir: t.TempDir(),
		Output:  output,
	})
	if err != nil {
		t.Fatalf("RunWithOptions() error = %v", err)
	}
	if strings.Contains(result.Output, "abc123456789") || output.contains("abc123456789") {
		t.Fatalf("output contains inherited secret: result=%q chunks=%+v", result.Output, output.chunks)
	}
	if !strings.Contains(result.Output, "out:<redacted>") || !output.has(StreamStdout, "out:<redacted>") {
		t.Fatalf("output missing redaction: result=%q chunks=%+v", result.Output, output.chunks)
	}
}

func TestOutputCollectorKeepsLimitedTail(t *testing.T) {
	collector := newOutputCollector(5, defaultAdapter{}, "")
	collector.Write("0123456789")
	if got := collector.Tail(); got != "56789" {
		t.Fatalf("Tail() = %q, want 56789", got)
	}
}

func TestBroadcasterDeliversChunksToSubscribers(t *testing.T) {
	broadcaster := NewBroadcaster()
	ch, unsubscribe := broadcaster.Subscribe(1)
	defer unsubscribe()

	if err := broadcaster.WriteOutput(OutputChunk{Stream: StreamStdout, Chunk: "live\n"}); err != nil {
		t.Fatalf("WriteOutput() error = %v", err)
	}

	got := <-ch
	if got.Stream != StreamStdout || got.Chunk != "live\n" {
		t.Fatalf("subscriber chunk = %+v, want stdout live chunk", got)
	}
}

func TestBroadcasterDoesNotBlockOnFullSubscriber(t *testing.T) {
	broadcaster := NewBroadcaster()
	ch, unsubscribe := broadcaster.Subscribe(1)
	defer unsubscribe()

	if err := broadcaster.WriteOutput(OutputChunk{Stream: StreamStdout, Chunk: "first\n"}); err != nil {
		t.Fatalf("WriteOutput() error = %v", err)
	}
	done := make(chan error, 1)
	go func() {
		done <- broadcaster.WriteOutput(OutputChunk{Stream: StreamStdout, Chunk: "second\n"})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("WriteOutput() error = %v", err)
		}
	case <-time.After(200 * time.Millisecond):
		<-ch
		if err := <-done; err != nil {
			t.Fatalf("WriteOutput() error = %v", err)
		}
		t.Fatal("WriteOutput blocked on a full subscriber")
	}
}

func TestCodexAdapterResumesExecSession(t *testing.T) {
	got := adapterFor(config.AgentConfig{Bin: "codex"}).Invocation([]string{"exec"}, "do work", "abc-123")
	want := []string{"exec", "resume", "abc-123", "--json", "do work"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestCodexAdapterExtractsThreadID(t *testing.T) {
	output := `{"type":"thread.started","thread_id":"019f2cd5-9288-73b1-bf76-8ac3f2e8ce7a"}`
	got := adapterFor(config.AgentConfig{Bin: "codex"}).ExtractSessionID(output)
	want := "019f2cd5-9288-73b1-bf76-8ac3f2e8ce7a"
	if got != want {
		t.Fatalf("ExtractSessionID() = %q, want %q", got, want)
	}
}

func TestClaudeAdapterAddsSessionIDForNewSession(t *testing.T) {
	got := adapterFor(config.AgentConfig{Bin: "claude"}).Invocation([]string{"-p"}, "do work", "")
	if got.SessionID == "" {
		t.Fatal("SessionID empty")
	}
	if !contains(got.Args, "--session-id") {
		t.Fatalf("args missing --session-id: %#v", got.Args)
	}
}

func TestClaudeAdapterResumesSession(t *testing.T) {
	got := adapterFor(config.AgentConfig{Bin: "claude"}).Invocation([]string{"-p"}, "do work", "abc-123")
	want := []string{"-p", "--resume", "abc-123", "do work"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func TestOpenCodeAdapterResumesSession(t *testing.T) {
	got := adapterFor(config.AgentConfig{Bin: "opencode"}).Invocation([]string{"run"}, "do work", "abc-123")
	want := []string{"run", "--session", "abc-123", "--format", "json", "do work"}
	if !reflect.DeepEqual(got.Args, want) {
		t.Fatalf("args = %#v, want %#v", got.Args, want)
	}
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

type recordingOutputWriter struct {
	chunks []OutputChunk
}

func (w *recordingOutputWriter) WriteOutput(chunk OutputChunk) error {
	w.chunks = append(w.chunks, chunk)
	return nil
}

func (w *recordingOutputWriter) has(stream Stream, value string) bool {
	for _, chunk := range w.chunks {
		if chunk.Stream == stream && strings.Contains(chunk.Chunk, value) {
			return true
		}
	}
	return false
}

func (w *recordingOutputWriter) contains(value string) bool {
	for _, chunk := range w.chunks {
		if strings.Contains(chunk.Chunk, value) {
			return true
		}
	}
	return false
}
