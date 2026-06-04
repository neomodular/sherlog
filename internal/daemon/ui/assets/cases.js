// cases.js — the Cases view: every session, open first then closed, each closed
// case showing a one-line resolution summary (case-board-ui spec: case list;
// design D7). Clicking a card routes to its detail view.

import { api } from "./api.js";
import { esc, badge, fmtDate, html } from "./render.js";

function activeSuspects(s) {
  return (s.hypotheses || []).filter((h) => h.status === "active").length;
}

function liveProbes(s) {
  return (s.probes || []).filter((p) => !p.removed).length;
}

function card(s) {
  const closed = !!s.closed_at;
  const res = s.resolution;
  // A closed case shows its root cause / fix one-liner when solved; an unsolved
  // close is labeled as such so the archive distinguishes the two (design D4).
  let resLine = "";
  if (closed) {
    if (res && res.root_cause) {
      resLine = `<div class="resolution"><b>Root cause:</b> ${esc(res.root_cause)}${
        res.fix_summary ? ` — ${esc(res.fix_summary)}` : ""
      }</div>`;
    } else {
      resLine = `<div class="resolution muted">Closed without a recorded resolution.</div>`;
    }
  }
  return `
    <a class="case-card ${closed ? "closed" : ""}" href="#/case/${encodeURIComponent(s.id)}">
      <div class="desc">${esc(s.description || "(no description)")} ${badge(
    closed ? "closed" : "open",
    closed ? "closed" : "open"
  )}</div>
      <div class="meta">
        #${esc(s.id)} · ${esc(s.cwd || "?")} ·
        ${activeSuspects(s)} active suspects · ${liveProbes(s)} live probes ·
        ${(s.runs || []).length} runs · opened ${fmtDate(s.created_at)}
      </div>
      ${resLine}
    </a>`;
}

export async function renderCases(view) {
  view.innerHTML = `<p class="loading">Loading cases…</p>`;
  let sessions;
  try {
    sessions = await api.cases();
  } catch (e) {
    view.innerHTML = `<p class="error">Could not load cases: ${esc(e.message)}</p>`;
    return;
  }
  if (!sessions || sessions.length === 0) {
    view.innerHTML = `<h1>Cases</h1><p class="empty">No investigations yet. Start one with <code>/debug</code>.</p>`;
    return;
  }
  const open = sessions.filter((s) => !s.closed_at);
  const closed = sessions.filter((s) => s.closed_at);
  view.innerHTML = html([
    `<h1>Cases</h1>`,
    open.length ? `<div class="section-label">Open (${open.length})</div>` : "",
    open.map(card).join(""),
    closed.length ? `<div class="section-label">Closed (${closed.length})</div>` : "",
    closed.map(card).join(""),
  ]);
}
