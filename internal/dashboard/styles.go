package dashboard

const styles = `
:root {
	--bg: #f5f7f8;
	--panel: #ffffff;
	--line: #d8dee4;
	--line-strong: #b8c0c8;
	--text: #20262d;
	--muted: #68727d;
	--accent: #2f6f63;
	--accent-weak: #dcebe7;
	--danger: #9b2d30;
	--danger-weak: #f3dcdd;
	--warn: #8a5a00;
	--warn-weak: #f5e8c8;
	--ok: #1f7a3f;
	--ok-weak: #dceedd;
	--running: #315f9f;
	--running-weak: #dfe9f7;
	--shadow: 0 1px 2px rgba(32, 38, 45, 0.08);
	font-family: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
	color: var(--text);
	background: var(--bg);
}

* { box-sizing: border-box; }
body { margin: 0; min-width: 320px; }
a { color: inherit; }

.app-shell { min-height: 100vh; }
.topbar {
	height: 56px;
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 16px;
	padding: 0 24px;
	border-bottom: 1px solid var(--line);
	background: var(--panel);
	position: sticky;
	top: 0;
	z-index: 2;
}
.brand { font-weight: 700; text-decoration: none; letter-spacing: 0; }
.topbar__meta { display: flex; gap: 8px; align-items: center; flex-wrap: wrap; justify-content: flex-end; }
.pill {
	display: inline-flex;
	align-items: center;
	min-height: 28px;
	padding: 3px 9px;
	border: 1px solid var(--line);
	border-radius: 999px;
	font-size: 12px;
	font-weight: 600;
	color: var(--muted);
	background: #fbfcfd;
	text-decoration: none;
	white-space: nowrap;
}
.pill--link:hover { border-color: var(--accent); color: var(--accent); }

.app-body { display: grid; grid-template-columns: 180px minmax(0, 1fr); }
.sidenav {
	min-height: calc(100vh - 56px);
	display: flex;
	flex-direction: column;
	gap: 4px;
	padding: 18px 12px;
	border-right: 1px solid var(--line);
	background: #eef2f3;
}
.navitem {
	display: block;
	padding: 9px 11px;
	border-radius: 6px;
	color: var(--muted);
	text-decoration: none;
	font-size: 14px;
	font-weight: 650;
}
.navitem:hover, .navitem.is-active { background: var(--panel); color: var(--text); box-shadow: var(--shadow); }

.main-panel { min-width: 0; padding: 24px; }
.page-head {
	display: flex;
	align-items: flex-start;
	justify-content: space-between;
	gap: 16px;
	margin-bottom: 16px;
}
h1 { margin: 0; font-size: 24px; line-height: 1.2; letter-spacing: 0; }
h2 { margin: 0 0 12px; font-size: 15px; line-height: 1.3; letter-spacing: 0; }
p { margin: 4px 0 0; color: var(--muted); }

.button, .filter {
	display: inline-flex;
	align-items: center;
	justify-content: center;
	min-height: 34px;
	padding: 6px 11px;
	border: 1px solid var(--line-strong);
	border-radius: 6px;
	background: var(--panel);
	text-decoration: none;
	font-size: 13px;
	font-weight: 650;
	white-space: nowrap;
}
.button:hover, .filter:hover { border-color: var(--accent); color: var(--accent); }
.button--danger { border-color: #d8989b; color: var(--danger); }
.button--small { min-height: 30px; padding: 4px 9px; }
.inline-form { display: inline-flex; gap: 6px; margin: 0; }
.inline-form__input--number {
	width: 56px;
	min-height: 30px;
	padding: 4px 8px;
	border: 1px solid var(--line-strong);
	border-radius: 6px;
	background: var(--panel);
	font-size: 13px;
}
.filterbar { display: flex; flex-wrap: wrap; gap: 8px; justify-content: flex-end; }
.health-row {
	display: grid;
	grid-template-columns: repeat(4, minmax(0, 1fr));
	gap: 12px;
	margin-bottom: 16px;
}
.health-item, .metric {
	min-width: 0;
	padding: 12px;
	border: 1px solid var(--line);
	background: var(--panel);
	box-shadow: var(--shadow);
}
.health-item {
	display: grid;
	grid-template-columns: auto minmax(0, 1fr);
	gap: 4px 8px;
	align-items: center;
}
.health-item small {
	grid-column: 1 / -1;
	color: var(--muted);
	overflow-wrap: anywhere;
}
.metric-grid {
	display: grid;
	grid-template-columns: repeat(4, minmax(0, 1fr));
	gap: 12px;
	margin-bottom: 16px;
}
.metric span {
	display: block;
	color: var(--muted);
	font-size: 12px;
	font-weight: 700;
	text-transform: uppercase;
}
.metric strong { display: block; margin-top: 4px; font-size: 22px; }

.table-wrap {
	overflow-x: auto;
	border: 1px solid var(--line);
	background: var(--panel);
	box-shadow: var(--shadow);
}
.table-wrap--compact { box-shadow: none; }
.table-wrap--compact table { min-width: 520px; }
table { width: 100%; border-collapse: collapse; min-width: 760px; }
th, td {
	padding: 10px 12px;
	border-bottom: 1px solid var(--line);
	text-align: left;
	vertical-align: middle;
	font-size: 13px;
}
th {
	background: #f9fafb;
	color: var(--muted);
	font-size: 12px;
	text-transform: uppercase;
}
tbody tr:hover { background: #fbfcfd; }
.mono, .mono-link { font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace; font-size: 12px; }
.mono-link { color: var(--accent); text-decoration: none; }
.mono-link:hover { text-decoration: underline; }
.forgejo-link { color: var(--accent); font-weight: 650; text-decoration: none; }
.forgejo-link:hover { text-decoration: underline; }
.muted { color: var(--muted); }

.status {
	display: inline-flex;
	align-items: center;
	min-height: 24px;
	padding: 3px 8px;
	border: 1px solid var(--line);
	border-radius: 999px;
	background: #f4f5f6;
	font-size: 12px;
	font-weight: 700;
}
.status--success { color: var(--ok); background: var(--ok-weak); border-color: #b7d8bb; }
.status--danger { color: var(--danger); background: var(--danger-weak); border-color: #e5b9bb; }
.status--running { color: var(--running); background: var(--running-weak); border-color: #bfd0e8; }
.status--queued { color: var(--warn); background: var(--warn-weak); border-color: #e4cf97; }

.grid { display: grid; gap: 16px; margin-bottom: 16px; }
.grid.two { grid-template-columns: minmax(0, 1fr) minmax(0, 1fr); }
.panel, .empty-state, .error-panel {
	border: 1px solid var(--line);
	background: var(--panel);
	box-shadow: var(--shadow);
}
.panel { padding: 16px; }
.panel-head { display: flex; justify-content: space-between; gap: 12px; align-items: center; margin-bottom: 12px; }
.facts { display: grid; grid-template-columns: 100px minmax(0, 1fr); gap: 8px 12px; margin: 0; }
.facts dt { color: var(--muted); font-size: 12px; font-weight: 700; text-transform: uppercase; }
.facts dd { margin: 0; min-width: 0; overflow-wrap: anywhere; }
.agent-list { display: grid; gap: 16px; }
.agent-drawer { padding: 0; overflow: hidden; }
.agent-drawer__summary {
	margin-bottom: 0;
	padding: 16px 18px;
	cursor: pointer;
	list-style: none;
	user-select: none;
	transition: background 0.12s ease;
}
.agent-drawer__summary::-webkit-details-marker { display: none; }
.agent-drawer__summary:hover { background: var(--accent-weak); }
.agent-drawer__summary:focus-visible { outline: 2px solid var(--accent); outline-offset: -2px; }
.agent-drawer__title { display: flex; align-items: center; gap: 12px; min-width: 0; }
.agent-drawer__title h2 { line-height: 1.2; }
.agent-drawer__chevron {
	display: inline-flex;
	align-items: center;
	justify-content: center;
	width: 26px;
	height: 26px;
	flex: none;
	border-radius: 6px;
	background: var(--accent-weak);
	color: var(--accent);
	font-size: 12px;
	transition: transform 0.18s ease;
}
.agent-drawer[open] > .agent-drawer__summary .agent-drawer__chevron { transform: rotate(90deg); }
.agent-drawer[open] > .agent-drawer__summary { border-bottom: 1px solid var(--line); background: var(--bg); }
.agent-drawer[open] > .agent-drawer__summary:hover { background: var(--accent-weak); }
.agent-drawer__body { padding: 18px; }
.agent-card__grid { display: grid; grid-template-columns: minmax(0, 1fr) 280px; gap: 18px; }
.agent-card__side { display: grid; gap: 10px; align-content: start; }
.agent-reset-form { gap: 10px; align-items: center; margin-top: 12px; }
.agent-form {
	display: grid;
	grid-template-columns: repeat(2, minmax(0, 1fr));
	gap: 12px;
	margin-top: 16px;
	padding-top: 14px;
	border-top: 1px solid var(--line);
}
.agent-form label {
	display: grid;
	gap: 5px;
	color: var(--muted);
	font-size: 12px;
	font-weight: 700;
	text-transform: uppercase;
}
.agent-form input:not([type="checkbox"]), .agent-form textarea, .agent-form select {
	width: 100%;
	min-width: 0;
	border: 1px solid var(--line-strong);
	border-radius: 6px;
	padding: 8px 9px;
	color: var(--text);
	background: var(--panel);
	font: inherit;
	font-size: 13px;
	text-transform: none;
}
.agent-form textarea {
	resize: vertical;
	font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace;
}
.agent-form select {
	appearance: none;
	-webkit-appearance: none;
	padding-right: 34px;
	cursor: pointer;
	background-image: url("data:image/svg+xml,%3Csvg%20xmlns='http://www.w3.org/2000/svg'%20width='12'%20height='12'%20viewBox='0%200%2024%2024'%20fill='none'%20stroke='%2368727d'%20stroke-width='3'%20stroke-linecap='round'%20stroke-linejoin='round'%3E%3Cpath%20d='M6%209l6%206%206-6'/%3E%3C/svg%3E");
	background-repeat: no-repeat;
	background-position: right 11px center;
}
.agent-form input:not([type="checkbox"]):focus, .agent-form textarea:focus, .agent-form select:focus {
	outline: none;
	border-color: var(--accent);
	box-shadow: 0 0 0 3px var(--accent-weak);
}
.agent-form .check-row {
	display: flex;
	align-items: center;
	gap: 8px;
	min-height: 34px;
	text-transform: none;
}
.form-actions {
	grid-column: 1 / -1;
	display: flex;
	align-items: center;
	gap: 12px;
	flex-wrap: wrap;
}
.secret-field {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 10px;
	padding: 9px 10px;
	border: 1px solid var(--line);
	background: #fbfcfd;
}
.secret-field span { color: var(--muted); font-size: 12px; font-weight: 700; text-transform: uppercase; }
.command-preview {
	display: inline-block;
	max-width: 100%;
	font-family: "SFMono-Regular", Consolas, "Liberation Mono", monospace;
	font-size: 12px;
	white-space: normal;
	overflow-wrap: anywhere;
}
.validation-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
.validation-list li {
	display: grid;
	grid-template-columns: auto 82px minmax(0, 1fr);
	align-items: center;
	gap: 8px;
	font-size: 13px;
}
.validation-list strong { font-size: 12px; color: var(--muted); text-transform: uppercase; }
.validation-list em { font-style: normal; color: var(--muted); overflow-wrap: anywhere; }
.link-row { display: flex; flex-wrap: wrap; gap: 8px; margin-top: 14px; }
.link-list { display: grid; gap: 8px; }
.link-list a, .link-missing {
	display: flex;
	align-items: center;
	justify-content: space-between;
	gap: 10px;
	padding: 9px 10px;
	border: 1px solid var(--line);
	text-decoration: none;
	font-size: 13px;
}
.link-list a { color: var(--accent); background: #fbfcfd; }
.link-list a:hover { border-color: var(--accent); }
.link-list strong { color: var(--muted); font-size: 12px; font-weight: 700; }
.link-missing { color: var(--muted); background: #f7f8f9; }
.event-list { list-style: none; padding: 0; margin: 0; display: grid; gap: 8px; }
.event-list li { display: grid; grid-template-columns: 150px 140px minmax(0, 1fr); gap: 8px; font-size: 13px; }
.event-list span { color: var(--muted); }
.event-list em { font-style: normal; color: var(--muted); overflow-wrap: anywhere; }
.log-toolbar {
	display: flex;
	flex-wrap: wrap;
	align-items: center;
	gap: 10px;
	margin-bottom: 12px;
	font-size: 13px;
}
.log-toolbar input[type="search"] {
	min-width: 220px;
	min-height: 34px;
	padding: 6px 9px;
	border: 1px solid var(--line-strong);
	border-radius: 6px;
}
.log-toolbar label { display: inline-flex; align-items: center; gap: 5px; color: var(--muted); }
.log-view {
	margin: 0;
	max-height: 380px;
	overflow: auto;
	padding: 12px;
	background: #111820;
	color: #e7edf3;
	font-size: 12px;
	line-height: 1.5;
	white-space: pre-wrap;
}
.log-view--nowrap { white-space: pre; }
.log-view span { display: block; }
.page-head-actions { display: flex; align-items: center; gap: 10px; }
.resume-panel { margin-top: 16px; }
.resume-form { display: grid; gap: 12px; }
.resume-form__row { display: grid; grid-template-columns: 120px minmax(0, 1fr); gap: 8px; align-items: start; }
.resume-form__row--prompt { align-items: start; }
.resume-form__label { padding-top: 7px; color: var(--muted); font-size: 12px; font-weight: 700; text-transform: uppercase; }
.resume-form__input {
	width: 100%;
	min-height: 34px;
	padding: 6px 9px;
	border: 1px solid var(--line-strong);
	border-radius: 6px;
	font-size: 13px;
	font-family: inherit;
	background: var(--panel);
	color: var(--text);
}
.resume-form__textarea { min-height: 120px; resize: vertical; }
.resume-form__actions { display: flex; gap: 10px; justify-content: flex-end; }
.empty-state { padding: 26px; text-align: center; }
.empty-state h2 { margin-bottom: 4px; }
.empty-inline { margin: 0; color: var(--muted); }
.error-panel {
	display: flex;
	gap: 10px;
	align-items: center;
	margin-bottom: 16px;
	padding: 12px 14px;
	border-color: #e5b9bb;
	background: var(--danger-weak);
	color: var(--danger);
}
@media (max-width: 760px) {
	.topbar { height: auto; min-height: 56px; padding: 12px 16px; align-items: flex-start; }
	.app-body { display: block; }
	.sidenav {
		min-height: 0;
		display: flex;
		flex-direction: row;
		gap: 6px;
		overflow-x: auto;
		padding: 10px 12px;
		border-right: 0;
		border-bottom: 1px solid var(--line);
	}
	.navitem { white-space: nowrap; }
	.main-panel { padding: 16px; }
	.page-head { display: block; }
	.page-head > :last-child { margin-top: 12px; }
	.grid.two { grid-template-columns: 1fr; }
	.agent-card__grid { grid-template-columns: 1fr; }
	.agent-form { grid-template-columns: 1fr; }
	.health-row, .metric-grid { grid-template-columns: 1fr; }
	.event-list li { grid-template-columns: 1fr; gap: 2px; }
}
`
