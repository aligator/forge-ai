package dashboard

import (
	"net/http"
	"strconv"

	"codeberg.org/forge-ai/internal/runstore"
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
	if h.actions != nil {
		h.cancelRun(w, r)
		return
	}
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

func (h *Handler) cancelRun(w http.ResponseWriter, r *http.Request) {
	if h.actions == nil {
		http.Error(w, "operator actions not supported", http.StatusNotImplemented)
		return
	}
	if h.store == nil {
		http.Error(w, "run store not configured", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if !canCancel(run.Status) {
		http.Error(w, "run status cannot be canceled", http.StatusBadRequest)
		return
	}
	if !h.actions.CancelRunAs(r.Context(), runID, actorFromRequest(r)) {
		h.logger.Warn("dashboard cancel run failed", "run_id", runID)
	}
	http.Redirect(w, r, "/dashboard/runs/"+runID, http.StatusSeeOther)
}

func (h *Handler) retryRun(w http.ResponseWriter, r *http.Request) {
	if h.actions == nil {
		http.Error(w, "operator actions not supported", http.StatusNotImplemented)
		return
	}
	if h.store == nil {
		http.Error(w, "run store not configured", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	run, err := h.store.GetRun(r.Context(), runID)
	if err != nil {
		http.Error(w, "run not found", http.StatusNotFound)
		return
	}
	if !canRetry(run.Status) {
		http.Error(w, "run status cannot be retried", http.StatusBadRequest)
		return
	}
	newRunID, err := h.actions.RetryRun(r.Context(), runID, actorFromRequest(r))
	if err != nil {
		data := h.baseData(r, "Retry Error", "runs")
		data.Error = "Retry failed: " + err.Error()
		if !h.loadRunDetail(w, r, runID, &data) {
			return
		}
		h.renderStatus(w, r, http.StatusBadRequest, "run_detail", data)
		return
	}
	http.Redirect(w, r, "/dashboard/runs/"+newRunID, http.StatusSeeOther)
}

func (h *Handler) pauseQueue(w http.ResponseWriter, r *http.Request) {
	if h.actions == nil {
		http.Error(w, "operator actions not supported", http.StatusNotImplemented)
		return
	}
	h.actions.PauseQueue(r.Context(), actorFromRequest(r))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *Handler) resumeQueue(w http.ResponseWriter, r *http.Request) {
	if h.actions == nil {
		http.Error(w, "operator actions not supported", http.StatusNotImplemented)
		return
	}
	h.actions.ResumeQueue(r.Context(), actorFromRequest(r))
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func (h *Handler) setMaxConcurrent(w http.ResponseWriter, r *http.Request) {
	if h.actions == nil {
		http.Error(w, "operator actions not supported", http.StatusNotImplemented)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid form", http.StatusBadRequest)
		return
	}
	maxConcurrent, err := strconv.Atoi(r.FormValue("max_concurrent"))
	if err != nil {
		http.Error(w, "max_concurrent must be a number", http.StatusBadRequest)
		return
	}
	if err := h.actions.SetMaxConcurrent(r.Context(), maxConcurrent, actorFromRequest(r)); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/dashboard", http.StatusSeeOther)
}

func actorFromRequest(r *http.Request) string {
	for _, key := range []string{"X-Forwarded-User", "X-Remote-User", "Remote-User"} {
		if value := r.Header.Get(key); value != "" {
			return value
		}
	}
	return "internal"
}

func canCancel(status runstore.Status) bool {
	return status == runstore.StatusQueued || status == runstore.StatusRunning
}

func canRetry(status runstore.Status) bool {
	return status == runstore.StatusFailed || status == runstore.StatusCanceled
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
	data.CanCancel = data.CanOperate && canCancel(run.Status)
	data.CanRetry = data.CanOperate && canRetry(run.Status)
	return true
}
