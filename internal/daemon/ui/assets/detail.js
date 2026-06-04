// detail.js — the Case detail view: suspect board, probe registry, run timeline,
// recorded resolution (closed cases), an evidence list, and — while the session is
// open — a live SSE tail that appends events without a reload (case-board-ui spec:
// case detail + live evidence tail; design D7).

import { api } from "./api.js";
import { esc, badge, loc, fmtDate, fmtTime, eventBody, html } from "./render.js";
import { caseHeader } from "./diff.js";

// activeSource holds the current EventSource so navigating away can close it. A
// lingering stream would otherwise hold a daemon subscriber slot per visited case.
let activeSource = null;

// closeStream tears down any open SSE connection. The router calls this on every
// navigation so exactly one stream is ever live (design D3: callers release subs).
export function closeStream() {
  if (activeSource) {
    activeSource.close();
    activeSource = null;
  }
}

function resolutionPanel(sess) {
  const r = sess.resolution;
  if (!r) return "";
  return `
    <div class="panel" style="border-left:4px solid var(--ok)">
      <h2 style="margin-top:0">Resolution</h2>
      ${r.root_cause ? `<div><b>Root cause:</b> ${esc(r.root_cause)}</div>` : ""}
      ${r.fix_summary ? `<div><b>Fix:</b> ${esc(r.fix_summary)}</div>` : ""}
      ${
        r.confirmed_hypothesis_id
          ? `<div><b>Confirmed suspect:</b> <span class="pid">${esc(
              r.confirmed_hypothesis_id
            )}</span></div>`
          : ""
      }
      <div class="note">Closed ${fmtDate(r.closed_at)}</div>
    </div>`;
}

function suspectPanel(h) {
  return `
    <div class="panel ${h.status === "killed" ? "killed" : ""}" data-hid="${esc(h.id)}">
      <div class="statement"><span class="pid">${esc(h.id)}</span> ${esc(
    h.statement
  )} ${badge(h.status, h.status)}</div>
      ${h.note ? `<div class="note">${esc(h.note)}</div>` : ""}
    </div>`;
}

function probeRow(p) {
  return `
    <tr>
      <td>${loc(p.file, p.line)}</td>
      <td><span class="pid">${esc(p.id)}</span></td>
      <td>${esc(p.hypothesis_id || "—")}</td>
      <td>${p.removed ? badge("closed", "removed") : badge("open", "live")}</td>
      <td>${esc(p.note || "")}</td>
    </tr>`;
}

function runRow(r) {
  const status = r.closed_at
    ? badge("verdict", r.verdict || "closed")
    : badge("open", "open");
  return `
    <tr>
      <td><span class="pid">${esc(r.id)}</span></td>
      <td>${status}</td>
      <td>${fmtDate(r.started_at)}</td>
      <td>${r.closed_at ? fmtDate(r.closed_at) : "—"}</td>
    </tr>`;
}

// tailRow renders one evidence line for the live tail / evidence list. kindLabel
// distinguishes streamed board/run/probe events from raw log hits; truncated marks
// a hit from a flood-truncated bucket so a partial tail is never mistaken for the
// complete probe history (spec: live evidence tail honors flood-control truncation).
function tailRow(ts, probe, body, kindLabel, isNew, truncated) {
  return `
    <div class="row ${isNew ? "new" : ""}">
      <span class="ts">${fmtTime(ts)}</span>
      <span class="probe">${esc(probe || "")}${truncated ? " " + badge("truncated", "truncated") : ""}</span>
      <span class="body">${kindLabel ? `<span class="kind">${esc(kindLabel)}</span> ` : ""}${body}</span>
    </div>`;
}

// seedEvidence renders the retained events already on record (the non-live
// history) so a closed case — or an open one just opened — shows evidence
// immediately, before any new SSE event arrives.
async function seedEvidence(tail, sess) {
  let results;
  try {
    results = await api.query(sess.id);
  } catch {
    tail.innerHTML = `<p class="muted">No evidence on record yet.</p>`;
    return;
  }
  // Flatten (run,probe) buckets into chronological rows; query returns retained
  // first/last-N per bucket, so a flood-truncated bucket is partial by design —
  // carry its truncated flag onto each row so the tail badges it (task 4.3).
  const rows = [];
  for (const b of results || []) {
    for (const ev of b.events || []) {
      rows.push({ ts: ev.ts, probe: ev.probe, body: eventBody(ev), truncated: !!b.truncated });
    }
  }
  rows.sort((x, y) => new Date(x.ts) - new Date(y.ts));
  if (rows.length === 0) {
    tail.innerHTML = `<p class="muted">No probe hits recorded.</p>`;
    return;
  }
  tail.innerHTML = rows.map((r) => tailRow(r.ts, r.probe, r.body, "", false, r.truncated)).join("");
  tail.scrollTop = tail.scrollHeight;
}

// startStream subscribes to the session's SSE feed and appends incoming events to
// the tail (case-board-ui spec: watching a reproduction live). The board section
// is re-rendered on a board event so hypothesis status updates appear without a
// reload. EventSource reconnects natively; closeStream() releases the subscriber.
function startStream(view, tail, dot, sess) {
  const source = api.events(sess.id);
  activeSource = source;
  source.onopen = () => dot.classList.add("on");
  source.onerror = () => dot.classList.remove("on"); // reconnecting; native retry

  const append = (ts, probe, body, kindLabel) => {
    if (tail.querySelector(".muted")) tail.innerHTML = "";
    tail.insertAdjacentHTML("beforeend", tailRow(ts, probe, body, kindLabel, true));
    tail.scrollTop = tail.scrollHeight;
  };

  source.addEventListener("log", (e) => {
    const ev = JSON.parse(e.data).payload || {};
    append(ev.ts, ev.probe, eventBody(ev), "");
  });
  source.addEventListener("run", (e) => {
    const run = JSON.parse(e.data).payload || {};
    append(
      run.started_at || new Date().toISOString(),
      run.id,
      run.closed_at ? `run closed: ${esc(run.verdict || "")}` : "run opened",
      "run"
    );
  });
  source.addEventListener("probe", (e) => {
    const p = JSON.parse(e.data).payload || {};
    append(new Date().toISOString(), p.id, `probe ${p.removed ? "removed" : "registered"} @ ${esc(p.file)}:${esc(p.line)}`, "probe");
  });
  source.addEventListener("board", (e) => {
    const h = JSON.parse(e.data).payload || {};
    append(new Date().toISOString(), h.id, `${esc(h.statement || "")} → ${esc(h.status || "")}`, "board");
    // Reflect the status change in the suspect board in place when the panel exists.
    const panel = view.querySelector(`.panel[data-hid="${CSS.escape(h.id)}"]`);
    if (panel) panel.outerHTML = suspectPanel(h);
  });
}

export async function renderDetail(view, id) {
  view.innerHTML = `<p class="loading">Loading case…</p>`;
  let sess;
  try {
    sess = await api.session(id);
  } catch (e) {
    view.innerHTML = `<p class="error">Could not load case #${esc(id)}: ${esc(e.message)}</p>`;
    return;
  }

  const open = !sess.closed_at;
  const hyps = sess.hypotheses || [];
  const probes = sess.probes || [];
  const runs = sess.runs || [];

  view.innerHTML = html([
    caseHeader(sess),
    `<div class="crumbs">#${esc(sess.id)} · ${esc(sess.cwd || "?")} · opened ${fmtDate(
      sess.created_at
    )} · ${open ? badge("open", "open") : badge("closed", "closed")}</div>`,
    `<div class="tabs">
       <a href="#/case/${esc(sess.id)}" class="active">Detail</a>
       <a href="#/case/${esc(sess.id)}/diff">Compare runs</a>
     </div>`,
    resolutionPanel(sess),

    `<h2>Suspects (${hyps.length})</h2>`,
    hyps.length ? hyps.map(suspectPanel).join("") : `<p class="empty">No suspects on the board yet.</p>`,

    `<h2>Probes (${probes.length})</h2>`,
    probes.length
      ? `<table><thead><tr><th>Location</th><th>Probe</th><th>Suspect</th><th>Status</th><th>Note</th></tr></thead><tbody>${probes
          .map(probeRow)
          .join("")}</tbody></table>`
      : `<p class="empty">No probes registered.</p>`,

    `<h2>Runs (${runs.length})</h2>`,
    runs.length
      ? `<table><thead><tr><th>Run</th><th>Verdict</th><th>Started</th><th>Closed</th></tr></thead><tbody>${runs
          .map(runRow)
          .join("")}</tbody></table>`
      : `<p class="empty">No runs yet.</p>`,

    `<h2>Evidence ${
      open ? `<span class="live-dot" id="liveDot" title="live"></span>` : ""
    }</h2>`,
    `<div class="tail" id="tail"><p class="loading">Loading evidence…</p></div>`,
  ]);

  const tail = view.querySelector("#tail");
  await seedEvidence(tail, sess);

  // Only open cases stream: a closed case's evidence is final, so no subscriber is
  // needed (and the daemon would never publish for it).
  if (open) {
    const dot = view.querySelector("#liveDot");
    startStream(view, tail, dot, sess);
  }
}
