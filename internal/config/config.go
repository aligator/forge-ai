package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

type AgentRoute struct {
	Mention  string
	User     string // Forgejo username this agent posts as; empty = global bootstrap user
	Password string // Forgejo password for auto-generating token at startup; empty = skip
	Token    string // Forgejo token for this agent; empty = global token
	Agent    AgentConfig
}

type Config struct {
	HTTPAddr                string
	ForgejoURL              string
	ForgejoToken            string
	ForgejoBootstrapUser    string
	ForgejoBootstrapPass    string
	ForgejoBootstrapToken   string
	ForgejoBootstrapEnabled bool
	CloneURLBase            string
	WebhookSecret           string
	Agents                  []AgentRoute
	AgentToolHints          string
	WorkspaceDir            string
	BranchPrefix            string
	CreatePR                bool
	MaxConcurrent           int
	AgentAllowGit           bool
	Git                     GitConfig
}

type AgentConfig struct {
	Bin             string
	Args            []string
	CommandTemplate string
	Timeout         time.Duration
}

type GitConfig struct {
	RemoteName string
	UserName   string
	UserEmail  string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	forgejoToken, err := tokenFromEnv()
	if err != nil {
		return Config{}, err
	}
	agentAllowGit := envBool("AGENT_ALLOW_GIT", false)

	cfg := Config{
		HTTPAddr:                env("HTTP_ADDR", ":8080"),
		ForgejoURL:              strings.TrimRight(env("FORGEJO_URL", "http://localhost:3000"), "/"),
		ForgejoToken:            forgejoToken,
		ForgejoBootstrapUser:    env("FORGEJO_BOOTSTRAP_USER", "forge-ai"),
		ForgejoBootstrapPass:    env("FORGEJO_BOOTSTRAP_PASSWORD", "forge-ai-password"),
		ForgejoBootstrapToken:   env("FORGEJO_BOOTSTRAP_TOKEN_NAME", "forge-ai-local"),
		ForgejoBootstrapEnabled: envBool("FORGEJO_BOOTSTRAP_TOKEN", true),
		CloneURLBase:            strings.TrimRight(env("CLONE_URL_BASE", "http://localhost:3000"), "/"),
		WebhookSecret:           os.Getenv("WEBHOOK_SECRET"),
		Agents:                  loadAgentRoutes(),
		AgentToolHints:          os.Getenv("AGENT_TOOL_HINTS"),
		WorkspaceDir:            env("WORKSPACE_DIR", ".forge-ai/workspaces"),
		BranchPrefix:            env("BRANCH_PREFIX", "forge-ai"),
		CreatePR:                envBool("CREATE_PR", true),
		MaxConcurrent:           envInt("MAX_CONCURRENT", 1),
		AgentAllowGit:           agentAllowGit,
		Git: GitConfig{
			RemoteName: env("GIT_REMOTE", "origin"),
			UserName:   env("GIT_USER_NAME", "forge-ai"),
			UserEmail:  env("GIT_USER_EMAIL", "forge-ai@example.invalid"),
		},
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loadAgentRoutes loads agent routes from numbered env vars (AGENT_0_USER, AGENT_0_BIN, ...).
// The mention is derived as "@"+user. Falls back to legacy TRIGGER_MENTION + AGENT_BIN/AGENT_COMMAND if no numbered routes found.
func loadAgentRoutes() []AgentRoute {
	var routes []AgentRoute
	for i := 0; ; i++ {
		prefix := fmt.Sprintf("AGENT_%d_", i)
		if os.Getenv(prefix+"USER") == "" {
			break
		}
		token := os.Getenv(prefix + "TOKEN")
		if token == "" {
			if tf := os.Getenv(prefix + "TOKEN_FILE"); tf != "" {
				if data, err := os.ReadFile(tf); err == nil {
					token = strings.TrimSpace(string(data))
				}
			}
		}
		user := os.Getenv(prefix + "USER")
		routes = append(routes, AgentRoute{
			Mention:  "@" + user,
			User:     user,
			Password: os.Getenv(prefix + "PASSWORD"),
			Token:    token,
			Agent: AgentConfig{
				Bin:             os.Getenv(prefix + "BIN"),
				Args:            fields(os.Getenv(prefix + "ARGS")),
				CommandTemplate: os.Getenv(prefix + "COMMAND"),
				Timeout:         envDuration(prefix+"TIMEOUT", 30*time.Minute),
			},
		})
	}
	if len(routes) > 0 {
		return routes
	}
	// Backward compat: single route from legacy vars
	return []AgentRoute{{
		Mention: env("TRIGGER_MENTION", "@forge-ai"),
		Agent: AgentConfig{
			Bin:             os.Getenv("AGENT_BIN"),
			Args:            fields(os.Getenv("AGENT_ARGS")),
			CommandTemplate: os.Getenv("AGENT_COMMAND"),
			Timeout:         envDuration("AGENT_TIMEOUT", 30*time.Minute),
		},
	}}
}

func (c Config) validate() error {
	var missing []string
	if c.ForgejoURL == "" {
		missing = append(missing, "FORGEJO_URL")
	}
	if c.ForgejoToken == "" && !c.ForgejoBootstrapEnabled {
		missing = append(missing, "FORGEJO_TOKEN")
	}
	if c.WorkspaceDir == "" {
		missing = append(missing, "WORKSPACE_DIR")
	}
	if c.MaxConcurrent <= 0 {
		return errors.New("MAX_CONCURRENT must be positive")
	}
	if len(c.Agents) == 0 {
		missing = append(missing, "AGENT_0_USER or TRIGGER_MENTION")
	}
	for i, route := range c.Agents {
		if route.User == "" && route.Mention == "" {
			missing = append(missing, fmt.Sprintf("AGENT_%d_USER", i))
		}
		if route.Agent.CommandTemplate == "" && route.Agent.Bin == "" {
			missing = append(missing, fmt.Sprintf("AGENT_%d_BIN or AGENT_%d_COMMAND", i, i))
		}
		if route.Agent.Timeout <= 0 {
			return fmt.Errorf("AGENT_%d_TIMEOUT must be positive", i)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("missing required config: %s", strings.Join(missing, ", "))
	}
	return nil
}

func tokenFromEnv() (string, error) {
	if token := os.Getenv("FORGEJO_TOKEN"); token != "" {
		return token, nil
	}
	tokenFile := os.Getenv("FORGEJO_TOKEN_FILE")
	if tokenFile == "" {
		return "", nil
	}
	content, err := os.ReadFile(tokenFile)
	if err != nil {
		return "", fmt.Errorf("read FORGEJO_TOKEN_FILE: %w", err)
	}
	return strings.TrimSpace(string(content)), nil
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func envBool(key string, fallback bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envDuration(key string, fallback time.Duration) time.Duration {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func fields(value string) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.Fields(value)
}
