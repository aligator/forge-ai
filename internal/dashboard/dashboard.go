package dashboard

import (
	"context"
	"embed"
	"html/template"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

//go:embed assets
var assetsFS embed.FS

type Store interface {
	ListRuns(context.Context, runstore.ListRunsOptions) ([]runstore.Run, error)
	GetRun(context.Context, string) (runstore.Run, error)
	ListEvents(context.Context, string) ([]runstore.Event, error)
	ListEventsSince(context.Context, string, int64) ([]runstore.Event, error)
	ListLogChunks(context.Context, string) ([]runstore.LogChunk, error)
	ListLogChunksSince(context.Context, string, int64) ([]runstore.LogChunk, error)
	ListLinks(context.Context, string) ([]runstore.Link, error)
	ListAuditEvents(context.Context, runstore.ListAuditEventsOptions) ([]runstore.AuditEvent, error)
	AddAuditEvent(context.Context, runstore.AuditEventInput) error
	GetAgentSettings(context.Context, string) (runstore.AgentSettings, error)
	UpsertAgentSettings(context.Context, runstore.UpsertAgentSettingsInput) (runstore.AgentSettings, error)
	DeleteAgentSettings(context.Context, string) error
}

// ManualResumer is implemented by service.Service and enables the dashboard to
// start and stop manual_resume runs directly without going through a webhook.
type ManualResumer interface {
	ManualResume(ctx context.Context, parentRunID, agentMention, sessionID, workspaceMode, prompt, createdBy string) (string, error)
	CancelRun(id string) bool
}

type OperatorActions interface {
	CancelRunAs(ctx context.Context, id, actor string) bool
	RetryRun(ctx context.Context, parentRunID, actor string) (string, error)
	PauseQueue(ctx context.Context, actor string)
	ResumeQueue(ctx context.Context, actor string)
}

type Handler struct {
	cfg      config.Config
	store    Store
	runtime  RuntimeSnapshotter
	resumer  ManualResumer
	actions  OperatorActions
	logger   *slog.Logger
	tmpl     *template.Template
	redactor dashboardRedactor

	healthMu      sync.Mutex
	healthCache   []healthItem
	healthCacheAt time.Time
}

type RuntimeSnapshotter interface {
	RuntimeSnapshot() appruntime.Snapshot
}

func New(cfg config.Config, store Store, logger *slog.Logger, runtime ...RuntimeSnapshotter) *Handler {
	if logger == nil {
		logger = slog.Default()
	}
	var snapshotter RuntimeSnapshotter
	if len(runtime) > 0 {
		snapshotter = runtime[0]
	}
	h := &Handler{
		cfg:      cfg,
		store:    store,
		runtime:  snapshotter,
		logger:   logger,
		redactor: newDashboardRedactor(cfg),
	}
	h.tmpl = template.Must(template.New("dashboard").Funcs(template.FuncMap{
		"formatTime":     formatTime,
		"formatDuration": formatDuration,
		"statusClass":    statusClass,
		"healthClass":    healthClass,
		"ticketURL":      forgejoTicketURL,
		"shortID":        shortID,
		"redactLog":      h.redactor.Redact,
		"dict":           dict,
	}).Parse(templates))
	return h
}

// WithResumer enables manual resume and stop functionality in the dashboard.
func (h *Handler) WithResumer(r ManualResumer) *Handler {
	h.resumer = r
	return h
}

func (h *Handler) WithOperatorActions(actions OperatorActions) *Handler {
	h.actions = actions
	return h
}

func (h *Handler) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /dashboard/assets/app.css", h.styles)
	mux.Handle("GET /dashboard/assets/htmx.min.js", http.StripPrefix("/dashboard", http.FileServerFS(assetsFS)))
	mux.HandleFunc("GET /dashboard", h.overview)
	mux.HandleFunc("GET /dashboard/runs", h.runs)
	mux.HandleFunc("GET /dashboard/runs/{id}", h.runDetail)
	mux.HandleFunc("GET /dashboard/runs/{id}/events", h.runEvents)
	mux.HandleFunc("POST /dashboard/runs/{id}/resume", h.resumeRun)
	mux.HandleFunc("POST /dashboard/runs/{id}/cancel", h.cancelRun)
	mux.HandleFunc("POST /dashboard/runs/{id}/retry", h.retryRun)
	mux.HandleFunc("POST /dashboard/runs/{id}/stop", h.stopRun)
	mux.HandleFunc("POST /dashboard/queue/pause", h.pauseQueue)
	mux.HandleFunc("POST /dashboard/queue/resume", h.resumeQueue)
	mux.HandleFunc("GET /dashboard/agents", h.agents)
	mux.HandleFunc("POST /dashboard/agents/{mention}/settings", h.saveAgentSettings)
	mux.HandleFunc("POST /dashboard/agents/{mention}/settings/reset", h.resetAgentSettings)
	mux.HandleFunc("GET /dashboard/audit", h.audit)
}
