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
		h.render(w, r, "run_detail", data)
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
	h.resumer.CancelRun(runID)
	http.Redirect(w, r, "/dashboard/runs/"+runID, http.StatusSeeOther)
}
