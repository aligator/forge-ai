package agent

import (
	"context"

	"codeberg.org/forge-ai/internal/config"
)

type codexAdapter struct{}

// codexModels seeds the dropdown when the codex model cache
// (~/.codex/models_cache.json) is absent, e.g. in a fresh container where codex
// has not yet fetched its model list. Free-text entry stays available.
var codexModels = []string{
	"gpt-5.5",
	"gpt-5.4",
	"gpt-5.4-mini",
}

func (codexAdapter) Invocation(baseArgs []string, prompt, sessionID string) Invocation {
	args := append([]string{}, baseArgs...)
	args = ensureFlag(args, "--json")
	if sessionID != "" {
		args = appendExecSubcommand(args, "resume", sessionID)
	}
	args = append(args, prompt)
	return Invocation{Args: args, SessionID: sessionID}
}

func (codexAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}

func (codexAdapter) AvailableModels(_ context.Context, _ config.AgentConfig) ([]string, error) {
	models, err := readCodexModels()
	if err == nil && len(models) > 0 {
		return models, nil
	}
	return append([]string(nil), codexModels...), err
}
