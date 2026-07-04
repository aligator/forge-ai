package agent

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"codeberg.org/forge-ai/internal/config"
)

type Invocation struct {
	Args      []string
	SessionID string
}

type Adapter interface {
	Invocation(baseArgs []string, prompt, sessionID string) Invocation
	ExtractSessionID(output string) string
}

type defaultAdapter struct{}

func (defaultAdapter) Invocation(baseArgs []string, prompt, _ string) Invocation {
	args := append([]string{}, baseArgs...)
	args = append(args, prompt)
	return Invocation{Args: args}
}

func (defaultAdapter) ExtractSessionID(output string) string {
	return extractSessionID(output)
}

var adaptersByType = map[string]Adapter{
	"codex":    codexAdapter{},
	"claude":   claudeAdapter{},
	"opencode": openCodeAdapter{},
}

func adapterFor(cfg config.AgentConfig) Adapter {
	if adapter, ok := adaptersByType[agentType(cfg)]; ok {
		return adapter
	}
	return defaultAdapter{}
}

func agentType(cfg config.AgentConfig) string {
	if strings.TrimSpace(cfg.Type) != "" {
		return strings.ToLower(strings.TrimSpace(cfg.Type))
	}
	return strings.ToLower(filepath.Base(cfg.Bin))
}

var sessionIDPattern = regexp.MustCompile(`(?i)(session[_ -]?id|session)[:= ]+["']?([A-Za-z0-9][A-Za-z0-9._:-]{3,})`)

func extractSessionID(output string) string {
	for line := range strings.SplitSeq(output, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var value any
		if json.Unmarshal([]byte(line), &value) == nil {
			if id := findSessionID(value); id != "" {
				return id
			}
		}
	}
	match := sessionIDPattern.FindStringSubmatch(output)
	if len(match) == 3 {
		return match[2]
	}
	return ""
}

func findSessionID(value any) string {
	switch v := value.(type) {
	case map[string]any:
		for _, key := range []string{"session_id", "sessionID", "sessionId", "thread_id", "threadID", "threadId"} {
			if id, ok := v[key].(string); ok && id != "" {
				return id
			}
		}
		if session, ok := v["session"].(map[string]any); ok {
			if id, ok := session["id"].(string); ok && id != "" {
				return id
			}
		}
		for _, child := range v {
			if id := findSessionID(child); id != "" {
				return id
			}
		}
	case []any:
		for _, child := range v {
			if id := findSessionID(child); id != "" {
				return id
			}
		}
	}
	return ""
}
