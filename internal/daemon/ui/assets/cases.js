// cases.js — the Cases view: every session, open first then closed, each closed
// case showing a one-line resolution summary (case-board-ui spec: case list;
// design D7). Clicking a card routes to its detail view.

import { api } from "./api.js";
import { esc, badge, fmtDay, brief, html } from "./render.js";

function activeSuspects(s) {
  return (s.hypotheses || []).filter((h) => h.status === "active").length;
}

function liveProbes(s) {
  return (s.probes || []).filter((p) => !p.removed).length;
}

// project shows just the working directory's basename in the card meta (the full
// path is a tooltip) — the list is a shelf of case spines, not a filesystem dump.
function project(cwd) {
  const base = String(cwd || "").split("/").filter(Boolean).pop();
  return base || "?";
}

// metaLine keeps the card's second line to what helps you pick a case: id,
// project, the counts that are non-zero noise-free (an open case shows its live
// investigation counts; a closed one only its runs), and the opened date.
function metaLine(s, closed) {
  const parts = [`#${esc(s.id)}`, `<span title="${esc(s.cwd || "")}">${esc(project(s.cwd))}</span>`];
  const runs = (s.runs || []).length;
  if (!closed) {
    parts.push(`${activeSuspects(s)} suspects`, `${liveProbes(s)} live probes`);
  }
  parts.push(`${runs} run${runs === 1 ? "" : "s"}`, `opened ${fmtDay(s.created_at)}`);
  return parts.join(" · ");
}

function card(s) {
  const closed = !!s.closed_at;
  const res = s.resolution;
  // A closed case shows a BRIEF root-cause teaser when solved (clamped in CSS as a
  // second line of defense); the full resolution lives in the detail view. An
  // unsolved close is labeled as such so the archive distinguishes the two (D4).
  let resLine = "";
  if (closed) {
    if (res && res.root_cause) {
      resLine = `<div class="resolution"><b>Root cause:</b> ${esc(brief(res.root_cause))}</div>`;
    } else {
      resLine = `<div class="resolution muted">Closed without a recorded resolution.</div>`;
    }
  }
  // The list shows the title (the scannable case identity), never the full
  // description (add-case-titles: lists show the title). The daemon always sends a
  // non-empty title — a real one or a description-derived fallback for legacy
  // cases — so the title field is the single source here.
  return `
    <a class="case-card ${closed ? "closed" : ""}" href="#/case/${encodeURIComponent(s.id)}">
      <div class="card-top">
        <span class="title">${esc(s.title || "(untitled case)")}</span>
        ${badge(closed ? "closed" : "open", closed ? "closed" : "open")}
      </div>
      <div class="meta">${metaLine(s, closed)}</div>
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
