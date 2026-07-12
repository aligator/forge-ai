package agent

import (
	"context"

	"github.com/google/uuid"

	"codeberg.org/forge-ai/internal/config"
)

type claudeAdapter struct{}

// claudeModels lists the Claude aliases and current model ids. The Claude CLI
// exposes no model-list command, so this curated set seeds the dropdown while
// free-text entry stays available for any newer model id.
var claudeModels = []string{
	"opus",
	"sonnet",
	"haiku",
	"fable",
	"claude-opus-4-8",
	"claude-sonnet-5",
	"claude-haiku-4-5",
	"claude-fable-5",
}

func (claudeAdapter) Invocation(baseArgs []string, prompt, sessionID string) Invocation {
	args := append([]string{}, baseArgs...)
	if sessionID != "" {
		args = append(args, "--resume", sessionID, prompt)
		return Invocation{Args: args, SessionID: sessionID}
	}
	generated := uuid.NewString()
	if generated != "" && !hasFlag(args, "--session-id") {
		args = append(args, "--session-id", generated)
	}
	args = append(args, prompt)
	return Invocation{Args: args, SessionID: generated}
}

func (claudeAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}

func (claudeAdapter) AvailableModels(ctx context.Context, cfg config.AgentConfig) ([]string, error) {
	models, queried, err := listAnthropicModels(ctx, cfg)
	if queried && err == nil && len(models) > 0 {
		return models, nil
	}
	return append([]string(nil), claudeModels...), err
}
