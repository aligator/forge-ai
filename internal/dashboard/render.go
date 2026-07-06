package dashboard

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

type pageData struct {
	Title       string
	Section     string
	Active      string
	ForgejoURL  string
	QueueLabel  string
	ModeLabel   string
	Agents      []config.AgentRoute
	Runs        []runstore.Run
	ActiveRuns  []runstore.Run
	RecentRuns  []runstore.Run
	FailedRuns  []runstore.Run
	Run         runstore.Run
	Events      []runstore.Event
	Logs        []runstore.LogChunk
	Links       []runstore.Link
	RunLinks    []runLinkItem
	Health      []healthItem
	Runtime     appruntime.Snapshot
	AgentCtx    agentContext
	Status      string
	Sort        string
	Direction   string
	Error       string
	Partial     bool
	GeneratedAt time.Time
}

func (h *Handler) renderRunError(w http.ResponseWriter, r *http.Request, err error, data pageData) {
	if errors.Is(err, sql.ErrNoRows) {
		data.Error = "Run not found."
		h.renderStatus(w, r, http.StatusNotFound, "run_detail", data)
		return
	}
	data.Error = "Run data is currently unavailable."
	h.logger.Warn("dashboard get run failed", "error", err)
	h.renderStatus(w, r, http.StatusInternalServerError, "run_detail", data)
}

func (h *Handler) render(w http.ResponseWriter, r *http.Request, section string, data pageData) {
	h.renderStatus(w, r, http.StatusOK, section, data)
}

func (h *Handler) renderStatus(w http.ResponseWriter, r *http.Request, status int, section string, data pageData) {
	data.Section = section
	data.Partial = r.Header.Get("HX-Request") == "true"
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if data.Partial {
		if err := h.tmpl.ExecuteTemplate(w, section, data); err != nil {
			h.logger.Error("render dashboard", "section", section, "error", err)
			return
		}
		if err := h.tmpl.ExecuteTemplate(w, "sidenav", data); err != nil {
			h.logger.Error("render dashboard nav", "section", section, "error", err)
		}
		return
	}
	if err := h.tmpl.ExecuteTemplate(w, "layout", data); err != nil {
		h.logger.Error("render dashboard", "section", section, "error", err)
	}
}

func (h *Handler) baseData(r *http.Request, title, active string) pageData {
	queue := fmt.Sprintf("Queue %d slot", h.cfg.MaxConcurrent)
	if h.cfg.MaxConcurrent != 1 {
		queue += "s"
	}
	mode := "Internal mode off"
	if !h.cfg.CreatePR {
		mode = "Internal mode on"
	}
	snap := h.runtimeSnapshot()
	return pageData{
		Title:       title,
		Active:      active,
		ForgejoURL:  h.cfg.ForgejoURL,
		QueueLabel:  queue,
		ModeLabel:   mode,
		Health:      h.health(r.Context()),
		Runtime:     snap,
		GeneratedAt: time.Now().UTC(),
	}
}

func (h *Handler) styles(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/css; charset=utf-8")
	w.Header().Set("Cache-Control", "public, max-age=300")
	_, _ = w.Write([]byte(styles))
}
