package dashboard

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

func (h *Handler) runEvents(w http.ResponseWriter, r *http.Request) {
	if h.store == nil {
		http.Error(w, "run store is not configured", http.StatusServiceUnavailable)
		return
	}
	runID := r.PathValue("id")
	if _, err := h.store.GetRun(r.Context(), runID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, "load run", http.StatusInternalServerError)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")

	var lastEventID, lastLogID int64
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		if err := h.writeNewRunMessages(r.Context(), w, runID, &lastEventID, &lastLogID); err != nil {
			h.logger.Warn("dashboard sse failed", "run_id", runID, "error", err)
			return
		}
		flusher.Flush()

		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
		}
	}
}

func (h *Handler) writeNewRunMessages(ctx context.Context, w http.ResponseWriter, runID string, lastEventID, lastLogID *int64) error {
	events, err := h.store.ListEventsSince(ctx, runID, *lastEventID)
	if err != nil {
		return err
	}
	for _, event := range events {
		event.Message = h.redactor.Redact(event.Message)
		event.DataJSON = h.redactor.Redact(event.DataJSON)
		if err := writeSSE(w, "run_event", event.ID, event); err != nil {
			return err
		}
		*lastEventID = event.ID
	}

	logs, err := h.store.ListLogChunksSince(ctx, runID, *lastLogID)
	if err != nil {
		return err
	}
	for _, chunk := range logs {
		chunk.Chunk = h.redactor.Redact(chunk.Chunk)
		if err := writeSSE(w, "run_log", chunk.ID, chunk); err != nil {
			return err
		}
		*lastLogID = chunk.ID
	}
	return nil
}

func writeSSE(w http.ResponseWriter, event string, id int64, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "id: %d\nevent: %s\ndata: %s\n\n", id, event, data)
	return err
}
