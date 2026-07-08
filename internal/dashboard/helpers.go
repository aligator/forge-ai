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

func formatDuration(start, finish time.Time) string {
	if start.IsZero() {
		return "-"
	}
	end := finish
	if end.IsZero() {
		end = time.Now().UTC()
	}
	d := end.Sub(start)
	if d < 0 {
		return "-"
	}
	if d < time.Second {
		return d.Round(time.Millisecond).String()
	}
	return d.Round(time.Second).String()
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

func healthClass(ok bool) string {
	if ok {
		return "status status--success"
	}
	return "status status--danger"
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

func dict(values ...any) map[string]any {
	out := make(map[string]any, len(values)/2)
	for i := 0; i+1 < len(values); i += 2 {
		key, ok := values[i].(string)
		if !ok {
			continue
		}
		out[key] = values[i+1]
	}
	return out
}
