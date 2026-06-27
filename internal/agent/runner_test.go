package agent

import "testing"

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
