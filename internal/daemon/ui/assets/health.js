// health.js — the Health view (#/health): daemon vitals, effective config with
// sources, storage, activity, and self-checks rendered from /api/stats (case-board-ui
// spec: Health view). It polls /api/stats every ~5s while the tab is visible (design
// D4: poll, not SSE — health data has no event source), pauses on a hidden tab via the
// Page Visibility API, ticks the uptime client-side between polls, and re-renders only
// when the data changes so the header never flickers (design D4 / Risks).

import { api } from "./api.js";
import { esc, fmtDate, html } from "./render.js";

// MASCOT is the exact header sprite (index.html .sprite). The status header reuses it
// verbatim so the healthy state shows the same glyph the user already knows as sherlog.
const MASCOT = `     ▄▄▄▄
 ▄▄████████▄▄
   ▐▛███▜▌
  ▝▜█████▛▘
    ▘▘ ▝▝`;

const POLL_MS = 5000;

// View-scoped state. timer drives polling; uptimeTimer ticks the uptime second-by-
// second; lastJSON is the last rendered payload for change-only re-render; startedMs
// and baseUptime anchor the client-side uptime tick to the last poll.
let timer = null;
let uptimeTimer = null;
let lastJSON = "";
let mounted = null; // the view element while the health page is active, else null
let startedAtMs = 0; // Date.parse of vitals.started_at, for the ticking uptime

// stopHealth tears down all timers and visibility listeners. The router calls it on
// every navigation so polling never outlives the view (mirrors detail.js closeStream).
export function stopHealth() {
  if (timer) {
    clearInterval(timer);
    timer = null;
  }
  if (uptimeTimer) {
    clearInterval(uptimeTimer);
    uptimeTimer = null;
  }
  document.removeEventListener("visibilitychange", onVisibility);
  mounted = null;
  lastJSON = "";
}

// onVisibility pauses polling on a hidden tab and resumes (with an immediate refresh)
// when the tab is shown again (case-board-ui spec: Hidden tab stops polling).
function onVisibility() {
  if (!mounted) return;
  if (document.hidden) {
    if (timer) {
      clearInterval(timer);
      timer = null;
    }
  } else if (!timer) {
    poll(); // refresh immediately on return, then resume the interval
    timer = setInterval(poll, POLL_MS);
  }
}

// poll fetches /api/stats and re-renders only when the payload changed. A fetch error
// shows an inline message without tearing down the timers, so a transient daemon blip
// recovers on the next tick.
async function poll() {
  if (!mounted) return;
  let stats;
  try {
    stats = await api.stats();
  } catch (e) {
    if (mounted) mounted.innerHTML = `<p class="error">Could not load health: ${esc(e.message)}</p>`;
    return;
  }
  if (!mounted) return; // navigated away while the request was in flight

  const json = JSON.stringify(stats);
  if (json === lastJSON) return; // change-only re-render (design D4)
  lastJSON = json;
  startedAtMs = Date.parse(stats.vitals && stats.vitals.started_at);
  mounted.innerHTML = renderStats(stats);
  tickUptime(); // paint the uptime immediately so it never shows a stale value
}

// failedChecks returns the self-checks reporting ok:false, in a stable key order so
// the header text does not jump between polls.
function failedChecks(checks) {
  return Object.keys(checks || {})
    .sort()
    .map((k) => checks[k])
    .filter((c) => c && c.ok === false);
}

// statusHeader is the mascot + "on the case" banner when every self-check passes, or
// the failing checks' detail text otherwise (case-board-ui spec: status header).
function statusHeader(stats) {
  const failed = failedChecks(stats.self_checks);
  const healthy = failed.length === 0;
  const message = healthy
    ? "on the case"
    : failed.map((c) => esc(c.detail)).join(" · ");
  return `
    <div class="health-status ${healthy ? "ok" : "fail"}">
      <pre class="sprite" aria-hidden="true">${esc(MASCOT)}</pre>
      <div class="status-msg">${message}</div>
    </div>`;
}

// kv builds one definition row for the panel tables.
function kv(label, value) {
  return `<div class="kv"><span class="k">${esc(label)}</span><span class="v">${value}</span></div>`;
}

// vitalsPanel shows process facts; uptime is rendered as a live-updating span the
// uptime tick rewrites every second (case-board-ui spec: live-ticking uptime).
function vitalsPanel(v) {
  return `
    <h2>Daemon</h2>
    <div class="panel health-grid">
      ${kv("Version", esc(v.version))}
      ${kv("Port", esc(v.port))}
      ${kv("PID", esc(v.pid))}
      ${kv("Started", esc(fmtDate(v.started_at)))}
      ${kv("Uptime", `<span id="uptime" class="mono">—</span>`)}
    </div>`;
}

// configPanel lists every effective config key with its value and source badge
// (case-board-ui spec: effective config with per-key sources). Keys are sorted so the
// table order is stable, excluding the sources map itself.
function configPanel(cfg) {
  const sources = cfg.sources || {};
  const rows = Object.keys(cfg)
    .filter((k) => k !== "sources")
    .sort()
    .map((k) => {
      const src = sources[k] || "default";
      return `
        <tr>
          <td class="mono">${esc(k)}</td>
          <td class="mono">${esc(String(cfg[k]))}</td>
          <td><span class="src src-${esc(src)}">${esc(src)}</span></td>
        </tr>`;
    })
    .join("");
  return `
    <h2>Configuration</h2>
    <table>
      <thead><tr><th>Key</th><th>Value</th><th>Source</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`;
}

// fmtBytes renders a byte count in human units for the storage panel.
function fmtBytes(n) {
  if (!n || n < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let v = n;
  let i = 0;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${i === 0 ? v : v.toFixed(1)} ${units[i]}`;
}

function storagePanel(s) {
  return `
    <h2>Storage</h2>
    <div class="panel health-grid">
      ${kv("Data directory", `<span class="mono">${esc(s.data_dir)}</span>`)}
      ${kv("Disk usage", esc(fmtBytes(s.disk_usage_bytes)))}
      ${kv("Open sessions", esc(s.open_sessions))}
      ${kv("Closed sessions", esc(s.closed_sessions))}
      ${kv("Total events", esc(s.total_events))}
      ${kv("Field notes", esc(s.field_notes))}
    </div>`;
}

function activityPanel(a) {
  const openRun = a.open_run
    ? `<a href="#/case/${encodeURIComponent(a.open_run.session)}">#${esc(a.open_run.session)} · ${esc(a.open_run.run)}</a>`
    : `<span class="muted">none</span>`;
  return `
    <h2>Activity</h2>
    <div class="panel health-grid">
      ${kv("Last event", a.last_event ? esc(fmtDate(a.last_event)) : `<span class="muted">no events yet</span>`)}
      ${kv("Events (last hour)", esc(a.hourly_events))}
      ${kv("Live Case Board streams", esc(a.subscribers))}
      ${kv("Open run", openRun)}
    </div>`;
}

// stalePanel is a one-line count linking to the existing Stale Probes view.
function stalePanel(n) {
  return `
    <h2>Probes</h2>
    <div class="panel">
      <a href="#/stale">${esc(n)} stale probe${n === 1 ? "" : "s"}</a> still registered.
    </div>`;
}

function renderStats(stats) {
  return html([
    `<h1>Health</h1>`,
    statusHeader(stats),
    vitalsPanel(stats.vitals || {}),
    configPanel(stats.config || {}),
    storagePanel(stats.storage || {}),
    activityPanel(stats.activity || {}),
    stalePanel(stats.stale_probes || 0),
  ]);
}

// fmtUptime renders a seconds count as a compact d/h/m/s string.
function fmtUptime(totalSeconds) {
  if (totalSeconds < 0) totalSeconds = 0;
  const d = Math.floor(totalSeconds / 86400);
  const h = Math.floor((totalSeconds % 86400) / 3600);
  const m = Math.floor((totalSeconds % 3600) / 60);
  const s = totalSeconds % 60;
  const parts = [];
  if (d) parts.push(`${d}d`);
  if (d || h) parts.push(`${h}h`);
  if (d || h || m) parts.push(`${m}m`);
  parts.push(`${s}s`);
  return parts.join(" ");
}

// tickUptime rewrites the uptime span from the started_at anchor and the local clock,
// so the value advances every second without polling the daemon (design D4).
function tickUptime() {
  if (!mounted) return;
  const span = mounted.querySelector("#uptime");
  if (!span || !startedAtMs) return;
  span.textContent = fmtUptime(Math.floor((Date.now() - startedAtMs) / 1000));
}

export async function renderHealth(view) {
  stopHealth(); // defensive: clear any leftover timers before mounting fresh
  mounted = view;
  view.innerHTML = `<h1>Health</h1><p class="loading">Loading health…</p>`;

  await poll(); // first paint
  if (!mounted) return; // navigated away during the first fetch

  timer = setInterval(poll, POLL_MS);
  uptimeTimer = setInterval(tickUptime, 1000);
  document.addEventListener("visibilitychange", onVisibility);
}
