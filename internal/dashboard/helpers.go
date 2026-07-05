package dashboard

import (
	"fmt"
	"strings"
	"time"

	"codeberg.org/forge-ai/internal/runstore"
)

func formatTime(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.UTC().Format("2006-01-02 15:04:05 UTC")
}

func statusClass(status runstore.Status) string {
	switch status {
	case runstore.StatusSuccess:
		return "status status--success"
	case runstore.StatusFailed, runstore.StatusCanceled, runstore.StatusTimeout:
		return "status status--danger"
	case runstore.StatusRunning:
		return "status status--running"
	case runstore.StatusQueued:
		return "status status--queued"
	default:
		return "status"
	}
}

func shortID(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

func forgejoTicketURL(base string, run runstore.Run) string {
	if base == "" || run.Owner == "" || run.Repo == "" || run.TicketKind == "" || run.TicketNumber == 0 {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + run.Owner + "/" + run.Repo + "/" + run.TicketKind + "s/" + fmt.Sprint(run.TicketNumber)
}
