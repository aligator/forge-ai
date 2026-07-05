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
		<span class="pill">{{.QueueLabel}}</span>
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
{{if .Runs}}
	{{template "runs_table" .}}
{{else}}
	{{template "empty_runs" .}}
{{end}}
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
			</tr>
			{{end}}
		</tbody>
	</table>
</div>
{{end}}

{{define "run_detail"}}
<section class="page-head">
	<div>
		<h1>Run {{shortID .Run.ID}}</h1>
		<p>{{.Run.Owner}}/{{.Run.Repo}} {{.Run.TicketKind}} #{{.Run.TicketNumber}}</p>
	</div>
	<span class="{{statusClass .Run.Status}}">{{.Run.Status}}</span>
</section>
{{template "error" .}}
{{if .Run.ID}}
<section class="grid two">
	<div class="panel">
		<h2>Run</h2>
		<dl class="facts">
			<dt>Branch</dt><dd class="mono">{{.Run.Branch}}</dd>
			<dt>Base</dt><dd class="mono">{{.Run.BaseBranch}}</dd>
			<dt>Agent</dt><dd>{{.Run.AgentMention}} <span class="muted">{{.Run.AgentType}}</span></dd>
			<dt>Session</dt><dd class="mono">{{if .Run.SessionID}}{{.Run.SessionID}}{{else}}-{{end}}</dd>
			<dt>Started</dt><dd>{{formatTime .Run.StartedAt}}</dd>
			<dt>Finished</dt><dd>{{formatTime .Run.FinishedAt}}</dd>
		</dl>
		{{if .Links}}
		<div class="link-row">
			{{range .Links}}<a class="button button--small" href="{{.URL}}">{{if .Label}}{{.Label}}{{else}}{{.Type}}{{end}}</a>{{end}}
		</div>
		{{end}}
	</div>
	<div class="panel">
		<h2>Events</h2>
		{{if .Events}}
		<ol class="event-list">
			{{range .Events}}<li><span>{{formatTime .Time}}</span><strong>{{.Type}}</strong>{{if .Message}}<em>{{.Message}}</em>{{end}}</li>{{end}}
		</ol>
		{{else}}
		<p class="empty-inline">No events recorded.</p>
		{{end}}
	</div>
</section>
<section class="panel">
	<div class="panel-head">
		<h2>Logs</h2>
		<span class="muted">SSE: /dashboard/runs/{{.Run.ID}}/events</span>
	</div>
	{{if .Logs}}
	<pre class="log-view">{{range .Logs}}[{{.Stream}}] {{.Chunk}}{{end}}</pre>
	{{else}}
	<p class="empty-inline">No log chunks recorded.</p>
	{{end}}
</section>
{{end}}
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
`
