package dashboard

const templates = `
{{define "layout"}}
<!doctype html>
<html lang="en">
<head>
	<meta charset="utf-8">
	<meta name="viewport" content="width=device-width, initial-scale=1">
	<title>{{.Title}} - Forge AI</title>
	<link rel="stylesheet" href="/dashboard/assets/app.css">
	<script src="/dashboard/assets/htmx.min.js" defer></script>
</head>
<body>
	<div class="app-shell">
		{{template "topbar" .}}
		<div class="app-body">
			{{template "sidenav" .}}
			<main id="main-panel" class="main-panel">
				{{template "content" .}}
			</main>
		</div>
	</div>
	<script>
	(function () {
		function text(node, value) {
			if (node) {
				node.textContent = value;
			}
		}
		function appendEvent(root, item) {
			var list = root.querySelector("[data-events]");
			var empty = root.querySelector("[data-events-empty]");
			if (!list) {
				return;
			}
			if (empty) {
				empty.remove();
			}
			var li = document.createElement("li");
			li.innerHTML = "<span></span><strong></strong><em></em>";
			text(li.children[0], new Date(item.Time).toISOString().replace("T", " ").replace(/\.\d+Z$/, " UTC"));
			text(li.children[1], item.Type || "");
			text(li.children[2], item.Message || "");
			list.appendChild(li);
		}
		function appendLog(root, item) {
			var viewer = root.querySelector("[data-log-view]");
			var empty = root.querySelector("[data-log-empty]");
			if (!viewer) {
				return;
			}
			if (empty) {
				empty.remove();
			}
			var line = "[" + (item.Stream || "combined") + "] " + (item.Chunk || "");
			var span = document.createElement("span");
			span.dataset.stream = item.Stream || "combined";
			span.textContent = line;
			viewer.appendChild(span);
			filterLogs(root);
			if (root.querySelector("[data-autoscroll]").checked) {
				viewer.scrollTop = viewer.scrollHeight;
			}
		}
		function filterLogs(root) {
			var q = (root.querySelector("[data-log-search]").value || "").toLowerCase();
			var streams = {};
			root.querySelectorAll("[data-stream-filter]").forEach(function (box) { streams[box.value] = box.checked; });
			root.querySelectorAll("[data-log-view] [data-stream]").forEach(function (line) {
				var matchesText = !q || line.textContent.toLowerCase().indexOf(q) >= 0;
				var stream = line.dataset.stream || "combined";
				var matchesStream = streams[stream] !== false;
				line.hidden = !(matchesText && matchesStream);
			});
		}
		document.addEventListener("input", function (event) {
			var root = event.target.closest("[data-run-detail]");
			if (root && event.target.matches("[data-log-search], [data-stream-filter]")) {
				filterLogs(root);
			}
		});
		document.addEventListener("change", function (event) {
			var root = event.target.closest("[data-run-detail]");
			if (!root) {
				return;
			}
			if (event.target.matches("[data-wrap]")) {
				root.querySelector("[data-log-view]").classList.toggle("log-view--nowrap", !event.target.checked);
			}
			if (event.target.matches("[data-stream-filter]")) {
				filterLogs(root);
			}
		});
		document.querySelectorAll("[data-run-detail]").forEach(function (root) {
			var url = root.dataset.eventsUrl;
			if (!url || !window.EventSource) {
				return;
			}
			var source = new EventSource(url);
			source.addEventListener("run_event", function (event) {
				var item = JSON.parse(event.data);
				appendEvent(root, item);
				if (item.Type) {
					var badge = root.querySelector("[data-run-status]");
					if (badge) {
						badge.textContent = item.Type;
						badge.className = "status status--" + (item.Type === "running" ? "running" : (item.Type === "queued" ? "queued" : (item.Type === "success" ? "success" : "danger")));
					}
				}
			});
			source.addEventListener("run_log", function (event) {
				appendLog(root, JSON.parse(event.data));
			});
		});
	}());
	</script>
</body>
</html>
{{end}}

{{define "content"}}
{{if eq .Section "overview"}}{{template "overview" .}}{{end}}
{{if eq .Section "runs"}}{{template "runs" .}}{{end}}
{{if eq .Section "run_detail"}}{{template "run_detail" .}}{{end}}
{{if eq .Section "agents"}}{{template "agents" .}}{{end}}
{{if eq .Section "audit"}}{{template "audit" .}}{{end}}
{{end}}

{{define "topbar"}}
<header class="topbar">
	<a class="brand" href="/dashboard">Forge AI</a>
	<div class="topbar__meta">
		<span class="pill">{{.Runtime.UsedSlots}}/{{.Runtime.MaxConcurrent}} slots</span>
		<span class="pill">Paused: {{if .Runtime.Paused}}yes{{else}}no{{end}}</span>
		<a class="pill pill--link" href="{{.ForgejoURL}}">Forgejo</a>
		<span class="pill">{{.ModeLabel}}</span>
	</div>
</header>
{{end}}

{{define "sidenav"}}
<nav id="sidenav" class="sidenav" aria-label="Dashboard"{{if .Partial}} hx-swap-oob="true"{{end}}>
	<a class="navitem {{if eq .Active "overview"}}is-active{{end}}" href="/dashboard">Overview</a>
	<a class="navitem {{if eq .Active "runs"}}is-active{{end}}" href="/dashboard/runs">Runs</a>
	<a class="navitem {{if eq .Active "agents"}}is-active{{end}}" href="/dashboard/agents">Agents</a>
	<a class="navitem {{if eq .Active "audit"}}is-active{{end}}" href="/dashboard/audit">Audit</a>
</nav>
{{end}}

{{define "overview"}}
<section class="page-head">
	<div>
		<h1>Operations</h1>
		<p>Current run activity and service status.</p>
	</div>
	<a class="button" href="/dashboard/runs" hx-get="/dashboard/runs" hx-target="#main-panel" hx-push-url="true">View runs</a>
</section>
{{template "error" .}}
{{template "health_row" .}}
<section class="metric-grid">
	<div class="metric"><span>Slots</span><strong>{{.Runtime.UsedSlots}}/{{.Runtime.MaxConcurrent}}</strong></div>
	<div class="metric"><span>Paused</span><strong>{{if .Runtime.Paused}}yes{{else}}no{{end}}</strong></div>
	<div class="metric"><span>Active tickets</span><strong>{{len .Runtime.ActiveTickets}}</strong></div>
	<div class="metric"><span>Blocked</span><strong>{{len .Runtime.BlockedRuns}}</strong></div>
</section>
<section class="grid two">
	<div class="panel">
		<h2>Active runs</h2>
		{{if .ActiveRuns}}{{template "runs_table_compact" .ActiveRuns}}{{else}}<p class="empty-inline">Keine aktiven Runs</p>{{end}}
	</div>
	<div class="panel">
		<h2>Recent failed runs</h2>
		{{if .FailedRuns}}{{template "runs_table_compact" .FailedRuns}}{{else}}<p class="empty-inline">Keine fehlgeschlagenen Runs</p>{{end}}
	</div>
</section>
<section class="panel">
	<h2>Last completed runs</h2>
	{{if .RecentRuns}}{{template "runs_table_compact" .RecentRuns}}{{else}}<p class="empty-inline">Keine abgeschlossenen Runs</p>{{end}}
</section>
{{end}}

{{define "runs"}}
<section class="page-head">
	<div>
		<h1>Runs</h1>
		<p>Recent webhook runs from the local run store.</p>
	</div>
	<div class="filterbar">
		<a class="filter" href="/dashboard/runs" hx-get="/dashboard/runs" hx-target="#main-panel" hx-push-url="true">All</a>
		<a class="filter" href="/dashboard/runs?status=queued" hx-get="/dashboard/runs?status=queued" hx-target="#main-panel" hx-push-url="true">Queued</a>
		<a class="filter" href="/dashboard/runs?status=running" hx-get="/dashboard/runs?status=running" hx-target="#main-panel" hx-push-url="true">Running</a>
		<a class="filter" href="/dashboard/runs?status=success" hx-get="/dashboard/runs?status=success" hx-target="#main-panel" hx-push-url="true">Success</a>
		<a class="filter" href="/dashboard/runs?status=failed" hx-get="/dashboard/runs?status=failed" hx-target="#main-panel" hx-push-url="true">Failed</a>
		<a class="filter" href="/dashboard/runs?sort=status&dir=asc" hx-get="/dashboard/runs?sort=status&dir=asc" hx-target="#main-panel" hx-push-url="true">Sort status</a>
		<a class="filter" href="/dashboard/runs?sort=agent&dir=asc" hx-get="/dashboard/runs?sort=agent&dir=asc" hx-target="#main-panel" hx-push-url="true">Sort agent</a>
	</div>
</section>
{{template "error" .}}
{{if .Runs}}
	{{template "runs_table" .}}
{{else}}
	{{template "empty_runs" .}}
{{end}}
{{end}}

{{define "runs_table"}}
<div class="table-wrap">
	<table>
		<thead>
			<tr>
				<th>Run</th>
				<th>Status</th>
				<th>Ticket</th>
				<th>Agent</th>
				<th>Branch</th>
				<th>Started</th>
				<th>Duration</th>
			</tr>
		</thead>
		<tbody>
			{{range .Runs}}
			<tr>
				<td><a class="mono-link" href="/dashboard/runs/{{.ID}}">{{.ID}}</a></td>
				<td><span class="{{statusClass .Status}}">{{.Status}}</span></td>
				<td>{{with ticketURL $.ForgejoURL .}}<a class="forgejo-link" href="{{.}}">{{end}}{{.Owner}}/{{.Repo}} {{.TicketKind}} #{{.TicketNumber}}{{with ticketURL $.ForgejoURL .}}</a>{{end}}</td>
				<td>{{.AgentMention}} <span class="muted">{{.AgentType}}</span></td>
				<td class="mono">{{.Branch}}</td>
				<td>{{formatTime .StartedAt}}</td>
				<td>{{formatDuration .StartedAt .FinishedAt}}</td>
			</tr>
			{{end}}
		</tbody>
	</table>
</div>
{{end}}

{{define "runs_table_compact"}}
<div class="table-wrap table-wrap--compact">
	<table>
		<thead><tr><th>Run</th><th>Status</th><th>Ticket</th><th>Agent</th><th>Duration</th></tr></thead>
		<tbody>
			{{range .}}
			<tr>
				<td><a class="mono-link" href="/dashboard/runs/{{.ID}}">{{shortID .ID}}</a></td>
				<td><span class="{{statusClass .Status}}">{{.Status}}</span></td>
				<td>{{.Owner}}/{{.Repo}} {{.TicketKind}} #{{.TicketNumber}}</td>
				<td>{{.AgentMention}}</td>
				<td>{{formatDuration .StartedAt .FinishedAt}}</td>
			</tr>
			{{end}}
		</tbody>
	</table>
</div>
{{end}}

{{define "run_detail"}}
<div data-run-detail data-events-url="/dashboard/runs/{{.Run.ID}}/events">
<section class="page-head">
	<div>
		<h1>Run {{shortID .Run.ID}}</h1>
		<p>{{.Run.Owner}}/{{.Run.Repo}} {{.Run.TicketKind}} #{{.Run.TicketNumber}}</p>
	</div>
	<span data-run-status class="{{statusClass .Run.Status}}">{{.Run.Status}}</span>
</section>
{{template "error" .}}
{{if .Run.ID}}
<section class="grid two">
	<div class="panel">
		<h2>Run summary</h2>
		<dl class="facts">
			<dt>Status</dt><dd><span class="{{statusClass .Run.Status}}">{{.Run.Status}}</span></dd>
			<dt>Branch</dt><dd class="mono">{{.Run.Branch}}</dd>
			<dt>Base</dt><dd class="mono">{{.Run.BaseBranch}}</dd>
			<dt>Agent</dt><dd>{{.Run.AgentMention}} <span class="muted">{{.Run.AgentType}}</span></dd>
			<dt>Duration</dt><dd>{{formatDuration .Run.StartedAt .Run.FinishedAt}}</dd>
			<dt>Session</dt><dd class="mono">{{if .Run.SessionID}}{{.Run.SessionID}}{{else}}-{{end}}</dd>
			<dt>Started</dt><dd>{{formatTime .Run.StartedAt}}</dd>
			<dt>Finished</dt><dd>{{formatTime .Run.FinishedAt}}</dd>
		</dl>
	</div>
	<div class="panel">
		<h2>Forgejo context</h2>
		<dl class="facts">
			<dt>Repository</dt><dd>{{.Run.Owner}}/{{.Run.Repo}}</dd>
			<dt>Ticket</dt><dd>{{.Run.TicketKind}} #{{.Run.TicketNumber}}</dd>
			<dt>Branch</dt><dd class="mono">{{.Run.Branch}}</dd>
			<dt>Base</dt><dd class="mono">{{.Run.BaseBranch}}</dd>
			<dt>Created by</dt><dd>{{if .Run.CreatedBy}}{{.Run.CreatedBy}}{{else}}-{{end}}</dd>
		</dl>
	</div>
</section>
<section class="grid two">
	<div class="panel">
		<h2>Agent context</h2>
		<dl class="facts">
			<dt>Mention</dt><dd>{{.AgentCtx.Mention}}</dd>
			<dt>Type</dt><dd>{{.AgentCtx.Type}}</dd>
			<dt>Command</dt><dd class="mono">{{if .AgentCtx.CommandPreview}}{{.AgentCtx.CommandPreview}}{{else}}-{{end}}</dd>
			<dt>Timeout</dt><dd>{{.AgentCtx.Timeout}}</dd>
			<dt>Git</dt><dd>AGENT_ALLOW_GIT={{.AgentCtx.AllowGit}}</dd>
		</dl>
	</div>
	<div class="panel">
		<h2>Forgejo links</h2>
		<div class="link-list">
			{{range .RunLinks}}
			{{if .Present}}<a href="{{.URL}}"><span>{{.Label}}</span><strong>{{.Type}}</strong></a>{{else}}<span class="link-missing"><span>{{.Label}}</span><strong>not recorded</strong></span>{{end}}
			{{end}}
		</div>
	</div>
</section>
<section class="grid two">
	<div class="panel">
		<h2>Timeline</h2>
		{{if .Events}}
		<ol class="event-list" data-events>
			{{range .Events}}<li><span>{{formatTime .Time}}</span><strong>{{.Type}}</strong>{{if .Message}}<em>{{redactLog .Message}}</em>{{end}}</li>{{end}}
		</ol>
		{{else}}
		<ol class="event-list" data-events></ol>
		<p class="empty-inline" data-events-empty>No events recorded.</p>
		{{end}}
	</div>
	<div class="panel">
		<h2>Session summary</h2>
		<dl class="facts">
			<dt>Session</dt><dd class="mono">{{if .Run.SessionID}}{{.Run.SessionID}}{{else}}-{{end}}</dd>
			<dt>Result</dt><dd>{{.Run.Status}}</dd>
			<dt>Error</dt><dd>{{if .Run.Error}}{{redactLog .Run.Error}}{{else}}-{{end}}</dd>
		</dl>
	</div>
</section>
<section class="panel">
	<div class="panel-head">
		<h2>Logs</h2>
		<span class="muted">SSE: /dashboard/runs/{{.Run.ID}}/events</span>
	</div>
	<div class="log-toolbar">
		<input data-log-search type="search" placeholder="Search logs">
		<label><input data-wrap type="checkbox" checked> Wrap</label>
		<label><input data-autoscroll type="checkbox" checked> Auto-scroll</label>
		<label><input data-stream-filter type="checkbox" value="stdout" checked> stdout</label>
		<label><input data-stream-filter type="checkbox" value="stderr" checked> stderr</label>
		<label><input data-stream-filter type="checkbox" value="combined" checked> combined</label>
	</div>
	{{if .Logs}}
	<pre class="log-view" data-log-view>{{range .Logs}}<span data-stream="{{.Stream}}">[{{.Stream}}] {{redactLog .Chunk}}</span>{{end}}</pre>
	{{else}}
	<pre class="log-view" data-log-view></pre>
	<p class="empty-inline" data-log-empty>No log chunks recorded.</p>
	{{end}}
</section>
{{end}}
</div>
{{end}}

{{define "agents"}}
<section class="page-head">
	<div>
		<h1>Agents</h1>
		<p>Configured mentions and execution backends.</p>
	</div>
</section>
{{if .Agents}}
<div class="table-wrap">
	<table>
		<thead><tr><th>Mention</th><th>User</th><th>Type</th><th>Command</th><th>Timeout</th></tr></thead>
		<tbody>
			{{range .Agents}}
			<tr>
				<td class="mono">{{.Mention}}</td>
				<td>{{.User}}</td>
				<td>{{.Agent.Type}}</td>
				<td class="mono">{{if .Agent.CommandTemplate}}{{.Agent.CommandTemplate}}{{else}}{{.Agent.Bin}} {{range .Agent.Args}}{{.}} {{end}}{{end}}</td>
				<td>{{.Agent.Timeout}}</td>
			</tr>
			{{end}}
		</tbody>
	</table>
</div>
{{else}}
<div class="empty-state"><h2>No agents configured</h2><p>Set AGENT_0_USER and agent command settings before accepting webhook work.</p></div>
{{end}}
{{end}}

{{define "audit"}}
<section class="page-head">
	<div>
		<h1>Audit</h1>
		<p>Recent run records for operational traceability.</p>
	</div>
</section>
{{template "error" .}}
{{if .Runs}}{{template "runs_table" .}}{{else}}{{template "empty_runs" .}}{{end}}
{{end}}

{{define "error"}}
{{if .Error}}<div class="error-panel" role="alert"><strong>Error</strong><span>{{.Error}}</span></div>{{end}}
{{end}}

{{define "empty_runs"}}
<div class="empty-state">
	<h2>No runs found</h2>
	<p>Webhook-triggered runs will appear here after they are accepted.</p>
</div>
{{end}}

{{define "health_row"}}
<section class="health-row">
	{{range .Health}}
	<div class="health-item">
		<span class="{{healthClass .OK}}">{{.Value}}</span>
		<strong>{{.Label}}</strong>
		<small>{{.Detail}}</small>
	</div>
	{{end}}
</section>
{{end}}
`
