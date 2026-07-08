package dashboard

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"codeberg.org/forge-ai/internal/runstore"
)

const maxAgentTimeout = 24 * time.Hour

type agentSettingsAuditData struct {
	Enabled     bool     `json:"enabled"`
	Model       string   `json:"model,omitempty"`
	Args        []string `json:"args,omitempty"`
	Timeout     string   `json:"timeout"`
	ToolHints   bool     `json:"tool_hints"`
	AllowGit    bool     `json:"allow_git,omitempty"`
	AllowGitSet bool     `json:"allow_git_set"`
}

func (h *Handler) saveAgentSettings(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "run store not configured", http.StatusServiceUnavailable)
		return
	}
	mention := r.PathValue("mention")
	if _, ok := h.routeForMention(mention); !ok {
		http.NotFound(w, r)
		return
	}
	input, err := h.parseAgentSettingsForm(r, mention)
	if err != nil {
		data := h.baseData(r, "Agents", "agents")
		data.Error = err.Error()
		h.renderStatus(w, r, http.StatusBadRequest, "agents", data)
		return
	}
	saved, err := h.store.UpsertAgentSettings(r.Context(), input)
	if err != nil {
		data := h.baseData(r, "Agents", "agents")
		data.Error = "Agent settings could not be saved."
		h.logger.Warn("dashboard save agent settings failed", "mention", mention, "error", err)
		h.renderStatus(w, r, http.StatusInternalServerError, "agents", data)
		return
	}
	if err := h.auditAgentSettingsChange(r, saved); err != nil {
		data := h.baseData(r, "Agents", "agents")
		data.Error = "Agent settings were saved, but audit logging failed."
		h.logger.Warn("dashboard audit agent settings failed", "mention", mention, "error", err)
		h.renderStatus(w, r, http.StatusInternalServerError, "agents", data)
		return
	}
	http.Redirect(w, r, "/dashboard/agents?saved="+mention, http.StatusSeeOther)
}

func (h *Handler) resetAgentSettings(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "run store not configured", http.StatusServiceUnavailable)
		return
	}
	mention := r.PathValue("mention")
	if _, ok := h.routeForMention(mention); !ok {
		http.NotFound(w, r)
		return
	}
	if err := h.store.DeleteAgentSettings(r.Context(), mention); err != nil {
		data := h.baseData(r, "Agents", "agents")
		data.Error = "Agent settings could not be reset."
		h.logger.Warn("dashboard reset agent settings failed", "mention", mention, "error", err)
		h.renderStatus(w, r, http.StatusInternalServerError, "agents", data)
		return
	}
	if err := h.auditAgentSettingsReset(r, mention); err != nil {
		data := h.baseData(r, "Agents", "agents")
		data.Error = "Agent settings were reset, but audit logging failed."
		h.logger.Warn("dashboard audit agent settings reset failed", "mention", mention, "error", err)
		h.renderStatus(w, r, http.StatusInternalServerError, "agents", data)
		return
	}
	http.Redirect(w, r, "/dashboard/agents?reset="+mention, http.StatusSeeOther)
}

func (h *Handler) parseAgentSettingsForm(r *http.Request, mention string) (runstore.UpsertAgentSettingsInput, error) {
	if err := r.ParseForm(); err != nil {
		return runstore.UpsertAgentSettingsInput{}, fmt.Errorf("parse form: %w", err)
	}
	timeout, err := time.ParseDuration(strings.TrimSpace(r.FormValue("timeout")))
	if err != nil {
		return runstore.UpsertAgentSettingsInput{}, errors.New("Timeout must be a Go duration such as 30m or 1h.")
	}
	args := strings.Fields(r.FormValue("args"))
	input := runstore.UpsertAgentSettingsInput{
		Mention:     mention,
		Enabled:     r.FormValue("enabled") == "on",
		Model:       strings.TrimSpace(r.FormValue("model")),
		Args:        args,
		Timeout:     timeout,
		ToolHints:   strings.TrimSpace(r.FormValue("tool_hints")),
		AllowGit:    r.FormValue("allow_git") == "on",
		AllowGitSet: r.FormValue("allow_git_set") == "on",
		UpdatedBy:   actorFromRequest(r),
	}
	if err := h.validateAgentSettings(input); err != nil {
		return runstore.UpsertAgentSettingsInput{}, err
	}
	return input, nil
}

func (h *Handler) validateAgentSettings(input runstore.UpsertAgentSettingsInput) error {
	if strings.TrimSpace(input.Mention) == "" {
		return errors.New("Agent mention is required.")
	}
	if input.Timeout <= 0 || input.Timeout > maxAgentTimeout {
		return errors.New("Timeout must be greater than 0 and no more than 24h.")
	}
	for _, arg := range input.Args {
		if strings.ContainsAny(arg, "\x00\r\n") {
			return errors.New("Args must be whitespace-separated tokens without control characters.")
		}
	}
	fields := []string{input.Model, strings.Join(input.Args, " "), input.ToolHints}
	for _, field := range fields {
		if h.containsDisallowedSecret(field) {
			return errors.New("Secrets are not allowed in model, args, or tool hints.")
		}
	}
	return nil
}

func (h *Handler) containsDisallowedSecret(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" {
		return false
	}
	redacted := h.redactor.Redact(value)
	if redacted != value {
		return true
	}
	lower := strings.ToLower(value)
	for _, marker := range []string{"api_key", "apikey", "access_token", "auth_token", "bearer ", "password", "secret", "token"} {
		if strings.Contains(lower, marker) {
			return true
		}
	}
	return false
}

func (h *Handler) auditAgentSettingsChange(r *http.Request, settings runstore.AgentSettings) error {
	data := agentSettingsAuditData{
		Enabled:     settings.Enabled,
		Model:       settings.Model,
		Args:        settings.Args,
		Timeout:     settings.Timeout.String(),
		ToolHints:   settings.ToolHints != "",
		AllowGit:    settings.AllowGit,
		AllowGitSet: settings.AllowGitSet,
	}
	dataJSON, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return h.store.AddAuditEvent(r.Context(), runstore.AuditEventInput{
		Actor:      actorFromRequest(r),
		Action:     "agent_settings.update",
		TargetType: "agent",
		TargetID:   settings.Mention,
		DataJSON:   string(dataJSON),
	})
}

func (h *Handler) auditAgentSettingsReset(r *http.Request, mention string) error {
	return h.store.AddAuditEvent(r.Context(), runstore.AuditEventInput{
		Actor:      actorFromRequest(r),
		Action:     "agent_settings.reset",
		TargetType: "agent",
		TargetID:   mention,
		DataJSON:   `{"source":"env"}`,
	})
}

func (h *Handler) routeForMention(mention string) (int, bool) {
	for i, route := range h.cfg.Agents {
		if strings.EqualFold(route.Mention, mention) {
			return i, true
		}
	}
	return 0, false
}
