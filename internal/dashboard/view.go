package dashboard

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"codeberg.org/forge-ai/internal/config"
	"codeberg.org/forge-ai/internal/runstore"
	appruntime "codeberg.org/forge-ai/internal/runtime"
)

type healthItem struct {
	Label  string
	Value  string
	OK     bool
	Detail string
}

type runSummary struct {
	ActiveRuns    []runstore.Run
	CompletedRuns []runstore.Run
	FailedRuns    []runstore.Run
}

type agentContext struct {
	Mention        string
	Type           string
	CommandPreview string
	Timeout        time.Duration
	AllowGit       bool
}

type runLinkItem struct {
	Type    string
	Label   string
	URL     string
	Present bool
}

type dashboardRedactor struct {
	secrets []string
	pattern *regexp.Regexp
}

const healthCacheTTL = 30 * time.Second

func (h *Handler) runtimeSnapshot() appruntime.Snapshot {
	if h.runtime == nil {
		return appruntime.Snapshot{MaxConcurrent: h.cfg.MaxConcurrent}
	}
	return h.runtime.RuntimeSnapshot()
}

func splitRunSummary(runs []runstore.Run) runSummary {
	summary := runSummary{}
	for _, run := range runs {
		switch run.Status {
		case runstore.StatusQueued, runstore.StatusRunning:
			summary.ActiveRuns = append(summary.ActiveRuns, run)
		case runstore.StatusFailed, runstore.StatusCanceled, runstore.StatusTimeout:
			summary.FailedRuns = append(summary.FailedRuns, run)
		default:
			summary.CompletedRuns = append(summary.CompletedRuns, run)
		}
	}
	return summary
}

func (h *Handler) health(ctx context.Context) []healthItem {
	now := time.Now()
	h.healthMu.Lock()
	if now.Sub(h.healthCacheAt) < healthCacheTTL && h.healthCache != nil {
		items := cloneHealthItems(h.healthCache)
		h.healthMu.Unlock()
		return items
	}
	h.healthMu.Unlock()

	items := []healthItem{
		h.forgejoHealth(ctx),
		{
			Label:  "Token",
			Value:  yesNo(h.hasToken()),
			OK:     h.hasToken(),
			Detail: "Forgejo token configured",
		},
		h.workspaceHealth(),
		h.agentBinaryHealth(),
	}

	h.healthMu.Lock()
	h.healthCache = cloneHealthItems(items)
	h.healthCacheAt = now
	h.healthMu.Unlock()
	return items
}

func cloneHealthItems(items []healthItem) []healthItem {
	out := make([]healthItem, len(items))
	copy(out, items)
	return out
}

func (h *Handler) forgejoHealth(ctx context.Context) healthItem {
	item := healthItem{Label: "Forgejo", Value: "not reachable", Detail: h.cfg.ForgejoURL}
	if strings.TrimSpace(h.cfg.ForgejoURL) == "" {
		return item
	}
	reqCtx, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodHead, h.cfg.ForgejoURL, nil)
	if err != nil {
		return item
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return item
	}
	_ = resp.Body.Close()
	item.OK = resp.StatusCode < 500
	if item.OK {
		item.Value = "reachable"
	}
	return item
}

func (h *Handler) hasToken() bool {
	if h.cfg.ForgejoToken != "" || h.cfg.ForgejoBootstrapEnabled {
		return true
	}
	for _, route := range h.cfg.Agents {
		if route.Token != "" {
			return true
		}
	}
	return false
}

func (h *Handler) workspaceHealth() healthItem {
	item := healthItem{Label: "Workspace", Value: "not writable", Detail: h.cfg.WorkspaceDir}
	if strings.TrimSpace(h.cfg.WorkspaceDir) == "" {
		return item
	}
	if err := os.MkdirAll(h.cfg.WorkspaceDir, 0o755); err != nil {
		return item
	}
	path := filepath.Join(h.cfg.WorkspaceDir, ".dashboard-health")
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		return item
	}
	_ = os.Remove(path)
	item.OK = true
	item.Value = "writable"
	return item
}

func (h *Handler) agentBinaryHealth() healthItem {
	item := healthItem{Label: "Agent binaries", Value: "missing", Detail: "No executable agent command found"}
	if len(h.cfg.Agents) == 0 {
		return item
	}
	var missing []string
	for _, route := range h.cfg.Agents {
		bin := route.Agent.Bin
		if bin == "" && route.Agent.CommandTemplate != "" {
			bin = strings.Fields(route.Agent.CommandTemplate)[0]
		}
		if bin == "" {
			missing = append(missing, route.Mention)
			continue
		}
		if _, err := exec.LookPath(bin); err != nil {
			missing = append(missing, bin)
		}
	}
	item.OK = len(missing) == 0
	if item.OK {
		item.Value = "found"
		item.Detail = "All configured agent binaries found"
		return item
	}
	item.Detail = strings.Join(missing, ", ")
	return item
}

func (h *Handler) agentContext(run runstore.Run) agentContext {
	ctx := agentContext{
		Mention:  run.AgentMention,
		Type:     run.AgentType,
		AllowGit: h.cfg.AgentAllowGit,
	}
	for _, route := range h.cfg.Agents {
		if strings.EqualFold(route.Mention, run.AgentMention) {
			ctx.Mention = route.Mention
			ctx.Type = firstNonEmpty(route.Agent.Type, run.AgentType)
			ctx.CommandPreview = h.redactor.Redact(commandPreview(route.Agent))
			ctx.Timeout = route.Agent.Timeout
			return ctx
		}
	}
	return ctx
}

func commandPreview(agent config.AgentConfig) string {
	if agent.CommandTemplate != "" {
		return agent.CommandTemplate
	}
	parts := append([]string{agent.Bin}, agent.Args...)
	return strings.TrimSpace(strings.Join(parts, " "))
}

func runLinks(base string, run runstore.Run, stored []runstore.Link) []runLinkItem {
	byType := make(map[string]runstore.Link, len(stored))
	for _, link := range stored {
		byType[link.Type] = link
	}
	items := []runLinkItem{
		linkItem("ticket", "Issue/PR", forgejoTicketURL(base, run), byType),
		linkItem("trigger_comment", "Trigger comment", "", byType),
		linkItem("branch", "Branch", forgejoBranchURL(base, run), byType),
		linkItem("pull_request", "Pull request", "", byType),
		linkItem("commit", "Commit", "", byType),
		linkItem("final_comment", "Final comment", "", byType),
	}
	return items
}

func linkItem(linkType, fallbackLabel, fallbackURL string, links map[string]runstore.Link) runLinkItem {
	item := runLinkItem{Type: linkType, Label: fallbackLabel, URL: fallbackURL, Present: fallbackURL != ""}
	if link, ok := links[linkType]; ok {
		item.URL = link.URL
		item.Present = strings.TrimSpace(link.URL) != ""
		item.Label = firstNonEmpty(link.Label, fallbackLabel)
	}
	return item
}

func forgejoBranchURL(base string, run runstore.Run) string {
	if base == "" || run.Owner == "" || run.Repo == "" || run.Branch == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/" + run.Owner + "/" + run.Repo + "/src/branch/" + url.PathEscape(run.Branch)
}

func normalizeSort(value string) string {
	switch value {
	case "started", "finished", "status", "agent", "ticket":
		return value
	default:
		return "started"
	}
}

func normalizeDirection(value string) bool {
	return value != "asc"
}

func newDashboardRedactor(cfg config.Config) dashboardRedactor {
	secrets := []string{cfg.ForgejoToken, cfg.WebhookSecret, cfg.ForgejoBootstrapPass}
	for _, route := range cfg.Agents {
		secrets = append(secrets, route.Token, route.Password)
	}
	return dashboardRedactor{
		secrets: compactSecrets(secrets),
		pattern: regexp.MustCompile(`(?i)(token|password|secret|authorization)(=|:|\s+bearer\s+)[^\s"'<>]+`),
	}
}

func compactSecrets(values []string) []string {
	var out []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if len(value) >= 8 {
			out = append(out, value)
		}
	}
	sort.Strings(out)
	return out
}

func (r dashboardRedactor) Redact(value string) string {
	for _, secret := range r.secrets {
		value = strings.ReplaceAll(value, secret, "<redacted>")
	}
	if r.pattern != nil {
		value = r.pattern.ReplaceAllStringFunc(value, func(match string) string {
			lower := strings.ToLower(match)
			for _, sep := range []string{" bearer ", "=", ":"} {
				if idx := strings.Index(lower, sep); idx >= 0 {
					return match[:idx+len(sep)] + "<redacted>"
				}
			}
			return "<redacted>"
		})
	}
	return value
}

func yesNo(value bool) string {
	if value {
		return "yes"
	}
	return "no"
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
