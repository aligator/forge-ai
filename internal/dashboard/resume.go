package dashboard

import (
	"net/http"
)

func (h *Handler) resumeRun(w http.ResponseWriter, r *http.Request) {
	if h.resumer == nil {
		http.Error(w, "manual resume not supported", http.StatusNotImplemented)
		return
	}
	if h.store == nil {
		http.Error(w, "run store not configured", http.StatusServiceUnavailable)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	parentRunID := r.PathValue("id")
	agentMention := r.FormValue("agent_mention")
	sessionID := r.FormValue("session_id")
	workspaceMode := r.FormValue("workspace_mode")
	prompt := r.FormValue("prompt")

	newRunID, err := h.resumer.ManualResume(r.Context(), parentRunID, agentMention, sessionID, workspaceMode, prompt, "operator")
	if err != nil {
		data := h.baseData(r, "Resume Error", "runs")
		data.Error = "Resume failed: " + err.Error()
		if !h.loadRunDetail(w, r, parentRunID, &data) {
			return
		}
		h.renderStatus(w, r, http.StatusBadRequest, "run_detail", data)
		return
	}
	http.Redirect(w, r, "/dashboard/runs/"+newRunID, http.StatusSeeOther)
}

func (h *Handler) stopRun(w http.ResponseWriter, r *http.Request) {
	if h.resumer == nil {
		http.Error(w, "cancel not supported", http.StatusNotImplemented)
		return
	}
	runID := r.PathValue("id")
	if !h.resumer.CancelRun(runID) {
		h.logger.Warn("dashboard cancel run failed", "run_id", runID)
	}
	http.Redirect(w, r, "/dashboard/runs/"+runID, http.StatusSeeOther)
}

func (h *Handler) loadRunDetail(w http.ResponseWriter, r *http.Request, runID string, data *pageData) bool {
	run, err := h.store.GetRun(r.Context(), runID)
	if err != nil {
		h.renderRunError(w, r, err, *data)
		return false
	}
	data.Title = "Run " + shortID(run.ID)
	data.Run = run
	data.Events, _ = h.store.ListEvents(r.Context(), run.ID)
	data.Logs, _ = h.store.ListLogChunks(r.Context(), run.ID)
	data.Links, _ = h.store.ListLinks(r.Context(), run.ID)
	data.RunLinks = runLinks(h.cfg.ForgejoURL, run, data.Links)
	data.AgentCtx = h.agentContext(run)
	return true
}
