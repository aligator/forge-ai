package agent

import (
	"reflect"
	"testing"

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
