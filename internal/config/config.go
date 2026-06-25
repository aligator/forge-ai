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
	TriggerMention          string
	WorkspaceDir            string
	BranchPrefix            string
	CreatePR                bool
	MaxConcurrent           int
	AgentAllowGit           bool
	Agent                   AgentConfig
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
		TriggerMention:          env("TRIGGER_MENTION", "@forge-ai"),
		WorkspaceDir:            env("WORKSPACE_DIR", ".forge-ai/workspaces"),
		BranchPrefix:            env("BRANCH_PREFIX", "forge-ai"),
		CreatePR:                envBool("CREATE_PR", true),
		MaxConcurrent:           envInt("MAX_CONCURRENT", 1),
		AgentAllowGit:           agentAllowGit,
		Agent: AgentConfig{
			Bin:             os.Getenv("AGENT_BIN"),
			Args:            fields(os.Getenv("AGENT_ARGS")),
			CommandTemplate: os.Getenv("AGENT_COMMAND"),
			Timeout:         envDuration("AGENT_TIMEOUT", 30*time.Minute),
		},
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

func (c Config) validate() error {
	var missing []string
	if c.ForgejoURL == "" {
		missing = append(missing, "FORGEJO_URL")
	}
	if c.ForgejoToken == "" && !c.ForgejoBootstrapEnabled {
		missing = append(missing, "FORGEJO_TOKEN")
	}
	if c.TriggerMention == "" {
		missing = append(missing, "TRIGGER_MENTION")
	}
	if c.WorkspaceDir == "" {
		missing = append(missing, "WORKSPACE_DIR")
	}
	if c.Agent.CommandTemplate == "" && c.Agent.Bin == "" {
		missing = append(missing, "AGENT_BIN or AGENT_COMMAND")
	}
	if c.Agent.Timeout <= 0 {
		return errors.New("AGENT_TIMEOUT must be positive")
	}
	if c.MaxConcurrent <= 0 {
		return errors.New("MAX_CONCURRENT must be positive")
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
