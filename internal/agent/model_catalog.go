package agent

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"codeberg.org/forge-ai/internal/config"
)

// ModelProvider is implemented by adapters that can enumerate the models their
// agent supports, whether by invoking the CLI, reading its model cache, or
// returning a curated static list.
type ModelProvider interface {
	AvailableModels(ctx context.Context, cfg config.AgentConfig) ([]string, error)
}

// SupportsModelListing reports whether the configured agent type can enumerate
// models.
func SupportsModelListing(cfg config.AgentConfig) bool {
	_, ok := adapterFor(cfg).(ModelProvider)
	return ok
}

// ListModels enumerates the models available for the configured agent. The
// second return value is false when the agent type cannot enumerate models; in
// that case the caller should fall back to free-text model entry.
func ListModels(ctx context.Context, cfg config.AgentConfig) ([]string, bool, error) {
	provider, ok := adapterFor(cfg).(ModelProvider)
	if !ok {
		return nil, false, nil
	}
	models, err := provider.AvailableModels(ctx, cfg)
	return models, true, err
}

func agentBin(cfg config.AgentConfig, fallback string) string {
	if bin := strings.TrimSpace(cfg.Bin); bin != "" {
		return bin
	}
	return fallback
}

// runModelListCommand invokes the agent binary with the given subcommand args
// and parses provider/model identifiers from its output.
func runModelListCommand(ctx context.Context, cfg config.AgentConfig, fallbackBin string, args ...string) ([]string, error) {
	cmd := exec.CommandContext(ctx, agentBin(cfg, fallbackBin), args...)
	if len(cfg.ExtraEnv) > 0 {
		cmd.Env = effectiveEnv(cfg.ExtraEnv...)
	}
	out, err := cmd.Output()
	models := parseProviderModelList(string(out))
	if err != nil && len(models) == 0 {
		return nil, err
	}
	return models, nil
}

var ansiPattern = regexp.MustCompile(`\x1b\[[0-9;]*m`)

// modelIDPattern matches provider/model identifiers such as
// "anthropic/claude-opus-4" while rejecting log or error lines.
var modelIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._:-]+$`)

func parseProviderModelList(output string) []string {
	seen := make(map[string]struct{})
	var models []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(ansiPattern.ReplaceAllString(line, ""))
		if !modelIDPattern.MatchString(line) {
			continue
		}
		if _, dup := seen[line]; dup {
			continue
		}
		seen[line] = struct{}{}
		models = append(models, line)
	}
	sort.Strings(models)
	return models
}

// codexModelsCache mirrors the parts of ~/.codex/models_cache.json we read.
type codexModelsCache struct {
	Models []struct {
		Slug string `json:"slug"`
	} `json:"models"`
}

// codexCachePath returns the path to the codex model cache, honoring CODEX_HOME.
func codexCachePath() string {
	home := strings.TrimSpace(os.Getenv("CODEX_HOME"))
	if home == "" {
		userHome, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		home = filepath.Join(userHome, ".codex")
	}
	return filepath.Join(home, "models_cache.json")
}

// envValue looks up a variable first in the agent's ExtraEnv, then in the
// process environment.
func envValue(cfg config.AgentConfig, key string) string {
	prefix := key + "="
	for _, kv := range cfg.ExtraEnv {
		if strings.HasPrefix(kv, prefix) {
			return kv[len(prefix):]
		}
	}
	return os.Getenv(key)
}

const anthropicModelsURL = "https://api.anthropic.com/v1/models?limit=1000"

type anthropicModelsResponse struct {
	Data []struct {
		ID string `json:"id"`
	} `json:"data"`
}

// listAnthropicModels queries the Claude API model list. It returns (nil, false, nil)
// when no API key is configured, signaling the caller to fall back to a static list.
func listAnthropicModels(ctx context.Context, cfg config.AgentConfig) ([]string, bool, error) {
	apiKey := strings.TrimSpace(envValue(cfg, "ANTHROPIC_API_KEY"))
	if apiKey == "" {
		return nil, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, anthropicModelsURL, nil)
	if err != nil {
		return nil, true, err
	}
	req.Header.Set("x-api-key", apiKey)
	req.Header.Set("anthropic-version", "2023-06-01")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, true, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, true, &modelListError{status: resp.StatusCode}
	}
	var parsed anthropicModelsResponse
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, true, err
	}
	models := make([]string, 0, len(parsed.Data))
	for _, m := range parsed.Data {
		if id := strings.TrimSpace(m.ID); id != "" {
			models = append(models, id)
		}
	}
	return models, true, nil
}

type modelListError struct{ status int }

func (e *modelListError) Error() string {
	return "model list request failed with status " + strconv.Itoa(e.status)
}

func readCodexModels() ([]string, error) {
	path := codexCachePath()
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var cache codexModelsCache
	if err := json.Unmarshal(data, &cache); err != nil {
		return nil, err
	}
	seen := make(map[string]struct{})
	var models []string
	for _, m := range cache.Models {
		slug := strings.TrimSpace(m.Slug)
		if slug == "" {
			continue
		}
		if _, dup := seen[slug]; dup {
			continue
		}
		seen[slug] = struct{}{}
		models = append(models, slug)
	}
	sort.Strings(models)
	return models, nil
}
