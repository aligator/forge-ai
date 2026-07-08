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
	Git      GitIdentity
	Agent    AgentConfig
	Disabled bool
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
	RunStorePath            string
	BranchPrefix            string
	CreatePR                bool
	MaxConcurrent           int
	AgentAllowGit           bool
	Git                     GitConfig
}

type AgentConfig struct {
	Type            string
	Bin             string
	Model           string
	Args            []string
	CommandTemplate string
	Timeout         time.Duration
	ToolHints       string
	AllowGit        bool
	AllowGitSet     bool
	ExtraEnv        []string // extra env vars injected into the agent subprocess (e.g. FORGEJO_ACCESS_TOKEN)
}

type GitConfig struct {
	RemoteName string
	GitIdentity
}

type GitIdentity struct {
	UserName  string
	UserEmail string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	forgejoToken, err := tokenFromEnv()
	if err != nil {
		return Config{}, err
	}
	agentAllowGit := envBool("AGENT_ALLOW_GIT", false)

	gitCfg := GitConfig{
		RemoteName: env("GIT_REMOTE", "origin"),
		GitIdentity: GitIdentity{
			UserName:  env("GIT_USER_NAME", "forge-ai"),
			UserEmail: env("GIT_USER_EMAIL", "forge-ai@example.invalid"),
		},
	}
	workspaceDir := env("WORKSPACE_DIR", ".forge-ai/workspaces")

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
		Agents:                  loadAgentRoutes(gitCfg.GitIdentity),
		AgentToolHints:          strings.ReplaceAll(os.Getenv("AGENT_TOOL_HINTS"), `\n`, "\n"),
		WorkspaceDir:            workspaceDir,
		RunStorePath:            env("RUNSTORE_PATH", workspaceDir+"/runstore.sqlite"),
		BranchPrefix:            env("BRANCH_PREFIX", "forge-ai"),
		CreatePR:                envBool("CREATE_PR", true),
		MaxConcurrent:           envInt("MAX_CONCURRENT", 1),
		AgentAllowGit:           agentAllowGit,
		Git:                     gitCfg,
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// loadAgentRoutes loads agent routes from numbered env vars (AGENT_0_USER, AGENT_0_BIN, ...).
// The mention is derived as "@" + user.
func loadAgentRoutes(defaultGit GitIdentity) []AgentRoute {
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
			Git: GitIdentity{
				UserName:  env(prefix+"GIT_USER_NAME", defaultGit.UserName),
				UserEmail: env(prefix+"GIT_USER_EMAIL", defaultGit.UserEmail),
			},
			Agent: AgentConfig{
				Type:            os.Getenv(prefix + "TYPE"),
				Bin:             os.Getenv(prefix + "BIN"),
				Model:           os.Getenv(prefix + "MODEL"),
				Args:            fields(os.Getenv(prefix + "ARGS")),
				CommandTemplate: os.Getenv(prefix + "COMMAND"),
				Timeout:         envDuration(prefix+"TIMEOUT", 30*time.Minute),
			},
		})
	}
	if len(routes) > 0 {
		return routes
	}
	return nil
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
		missing = append(missing, "AGENT_0_USER")
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
