package dashboard

import (
	"net/http"

	"codeberg.org/forge-ai/internal/runstore"
)

func (h *Handler) overview(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Overview", "overview")
	if h.store != nil {
		runs, err := h.store.ListRuns(r.Context(), runstore.ListRunsOptions{Limit: 50, Sort: "started", Desc: true})
		if err != nil {
			data.Error = "Run data is currently unavailable."
			h.logger.Warn("dashboard list runs failed", "error", err)
		} else {
			data.Runs = runs
			summary := splitRunSummary(runs)
			data.ActiveRuns = summary.ActiveRuns
			data.RecentRuns = summary.CompletedRuns
			data.FailedRuns = summary.FailedRuns
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
	data.Sort = normalizeSort(r.URL.Query().Get("sort"))
	desc := normalizeDirection(r.URL.Query().Get("dir"))
	data.Direction = "desc"
	if !desc {
		data.Direction = "asc"
	}
	runs, err := h.store.ListRuns(r.Context(), runstore.ListRunsOptions{Limit: 50, Status: status, Sort: data.Sort, Desc: desc})
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
	if !h.loadRunDetail(w, r, r.PathValue("id"), &data) {
		return
	}
	h.render(w, r, "run_detail", data)
}

func (h *Handler) agents(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Agents", "agents")
	h.render(w, r, "agents", data)
}

func (h *Handler) audit(w http.ResponseWriter, r *http.Request) {
	data := h.baseData(r, "Audit", "audit")
	if h.store != nil {
		events, err := h.store.ListAuditEvents(r.Context(), runstore.ListAuditEventsOptions{Limit: 100})
		if err != nil {
			data.Error = "Audit data is currently unavailable."
			h.logger.Warn("dashboard audit list failed", "error", err)
		} else {
			data.AuditEvents = events
		}
	}
	h.render(w, r, "audit", data)
}
