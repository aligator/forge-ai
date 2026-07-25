package config

import (
	"strings"

	"codeberg.org/forge-ai/internal/runstore"
)

func ApplyAgentSettings(cfg *Config, settings []runstore.AgentSettings) {
	byMention := make(map[string]runstore.AgentSettings, len(settings))
	for _, item := range settings {
		byMention[strings.ToLower(item.Mention)] = item
	}
	for i := range cfg.Agents {
		route := &cfg.Agents[i]
		setting, ok := byMention[strings.ToLower(route.Mention)]
		if !ok {
			continue
		}
		ApplyAgentSettingsToRoute(route, setting)
	}
}

func ApplyAgentSettingsToRoute(route *AgentRoute, settings runstore.AgentSettings) {
	route.Disabled = !settings.Enabled
	ApplyAgentSettingsToAgentConfig(&route.Agent, settings)
}

func ApplyAgentSettingsToAgentConfig(agent *AgentConfig, settings runstore.AgentSettings) {
	agent.Model = settings.Model
	agent.Args = append([]string(nil), settings.Args...)
	agent.Timeout = settings.Timeout
	agent.ToolHints = settings.ToolHints
	if settings.AllowGitSet {
		agent.AllowGit = settings.AllowGit
		agent.AllowGitSet = true
	}
}
