package dashboard

import (
	"net/http"

	"codeberg.org/forge-ai/internal/runstore"
)

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Overview", "overview")
	if h.store != nil {
		runs, err := h.store.ListRuns(r.Context(), runstore.ListRunsOptions{Limit: 8})
		if err != nil {
			data.Error = "Run data is currently unavailable."
			h.logger.Warn("dashboard list runs failed", "error", err)
		} else {
			data.Runs = runs
		}
	}
	h.render(w, r, "overview", data)
}

func (h *Handler) runs(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Runs", "runs")
	if h.store == nil {
		data.Error = "Run store is not configured."
		h.render(w, r, "runs", data)
		return
	}
	status := runstore.Status(r.URL.Query().Get("status"))
	data.Status = string(status)
	runs, err := h.store.ListRuns(r.Context(), runstore.ListRunsOptions{Limit: 50, Status: status})
	if err != nil {
		data.Error = "Run data is currently unavailable."
		h.logger.Warn("dashboard list runs failed", "error", err)
	} else {
		data.Runs = runs
	}
	h.render(w, r, "runs", data)
}

func (h *Handler) runDetail(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Run Detail", "runs")
	if h.store == nil {
		data.Error = "Run store is not configured."
		h.renderStatus(w, r, http.StatusServiceUnavailable, "run_detail", data)
		return
	}
	run, err := h.store.GetRun(r.Context(), r.PathValue("id"))
	if err != nil {
		h.renderRunError(w, r, err, data)
		return
	}
	data.Title = "Run " + shortID(run.ID)
	data.Run = run
	data.Events, _ = h.store.ListEvents(r.Context(), run.ID)
	data.Logs, _ = h.store.ListLogChunks(r.Context(), run.ID)
	data.Links, _ = h.store.ListLinks(r.Context(), run.ID)
	h.render(w, r, "run_detail", data)
}

func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Agents", "agents")
	data.Agents = h.cfg.Agents
	h.render(w, r, "agents", data)
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Audit", "audit")
	if h.store != nil {
		runs, err := h.store.ListRuns(r.Context(), runstore.ListRunsOptions{Limit: 20})
		if err != nil {
			data.Error = "Audit data is currently unavailable."
			h.logger.Warn("dashboard audit list failed", "error", err)
		} else {
			data.Runs = runs
		}
	}
	h.render(w, r, "audit", data)
}
