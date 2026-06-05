// stale.js — the Stale probes view: every registered-but-not-removed probe across
// all sessions, with session/file/line so a leftover probe line can be located
// and deleted (case-board-ui spec: stale probes — the browser `sherlog probes
// --stale`).

import { api } from "./api.js";
import { esc, loc, html } from "./render.js";

export async function renderStale(view) {
  view.innerHTML = `<p class="loading">Loading stale probes…</p>`;
  let stale;
  try {
    stale = await api.staleProbes();
  } catch (e) {
    view.innerHTML = `<p class="error">Could not load stale probes: ${esc(e.message)}</p>`;
    return;
  }
  if (!stale || stale.length === 0) {
    view.innerHTML = `<h1>Stale probes</h1><p class="empty">No leftover probes — every registered probe is marked removed.</p>`;
    return;
  }
  // Each row identifies its owning case by title (add-case-titles: case references
  // show the title), linking to the case detail. The daemon always sends a non-empty
  // session_title (real or derived); the #id stays as a small monospace tag so a
  // case is still locatable by ID.
  const rows = stale
    .map(
      (sp) => `
      <tr>
        <td>${loc(sp.probe.file, sp.probe.line)}</td>
        <td><span class="pid">${esc(sp.probe.id)}</span></td>
        <td><a href="#/case/${encodeURIComponent(sp.session_id)}">${esc(
        sp.session_title || sp.session_id
      )}</a> <span class="pid">#${esc(sp.session_id)}</span></td>
        <td>${esc(sp.probe.hypothesis_id || "—")}</td>
        <td>${esc(sp.probe.note || "")}</td>
      </tr>`
    )
    .join("");
  view.innerHTML = html([
    `<h1>Stale probes <span class="muted">(${stale.length})</span></h1>`,
    `<p class="muted">Probe lines still in your code. Delete each one and it disappears from this list once <code>remove_probe</code> runs.</p>`,
    `<table>
      <thead><tr><th>Location</th><th>Probe</th><th>Case</th><th>Suspect</th><th>Note</th></tr></thead>
      <tbody>${rows}</tbody>
    </table>`,
  ]);
}
