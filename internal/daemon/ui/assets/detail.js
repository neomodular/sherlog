// detail.js — the Case detail view: suspect board, probe registry, run timeline,
// recorded resolution (closed cases), an evidence list, and — while the session is
// open — a live SSE tail that appends events without a reload (case-board-ui spec:
// case detail + live evidence tail; design D7).

import { api } from "./api.js";
import {
  esc,
  badge,
  loc,
  fmtDate,
  fmtTime,
  eventBody,
  html,
  renderDescription,
  displayName,
  cleanStatement,
  hypothesisColor,
  hypChip,
  caseTabs,
} from "./render.js";
import { caseHeader } from "./diff.js";

// hypIndex maps every hypothesis id to its board position so display name, palette
// color, and status are resolvable from any view (suspect cards, probes table,
// verdict, evidence) without re-deriving them per call (polish-case-board D2). The
// index drives the color so a hypothesis keeps the same color everywhere it appears.
function hypIndex(hyps) {
  const map = new Map();
  (hyps || []).forEach((h, i) => map.set(h.id, { index: i, status: h.status }));
  return map;
}

// chipFor renders a hypothesis reference (colored dot + display name) for a given
// id using the board index — the one place every non-card reference resolves its
// color and state from. A reference to a hypothesis not on the board (shouldn't
// happen, but data can drift) falls back to a plain display name.
function chipFor(idx, id) {
  if (!id) return `<span class="muted">—</span>`;
  const meta = idx.get(id);
  if (!meta) return esc(displayName(id));
  return hypChip(id, hypothesisColor(meta.index), meta.status);
}

// citation renders a killed/confirmed hypothesis's evidence citation as display
// names — "Probe 1 · Run 2" — from the store's persisted evidence_probe_id /
// evidence_run_id (harden-detective-gates D-B). It links the verdict to the run it
// came from, beside the note. A hypothesis without a stored citation (legacy
// kills/confirms that predate the gate, or an active suspect) renders nothing.
function citation(h) {
  if (!h.evidence_probe_id || !h.evidence_run_id) return "";
  return `<span class="citation">${esc(displayName(h.evidence_probe_id))} · ${esc(
    displayName(h.evidence_run_id)
  )}</span>`;
}

// noteLine renders a hypothesis's evidence note and its citation together (the
// citation sits "alongside the note", harden-detective-gates: verdicts show their
// citation). Either may be absent; an entirely empty pair yields no element.
function noteLine(h) {
  const parts = [];
  if (h.note) parts.push(esc(cleanStatement(h.note)));
  const cite = citation(h);
  if (cite) parts.push(cite);
  return parts.length ? `<div class="note">${parts.join(" ")}</div>` : "";
}

// predictionPair renders a probe's discriminating prediction pair (if-true /
// if-false) visually paired so the contrast reads at a glance (harden-detective-gates
// D-A / case-board-ui). Both are escaped (probe text is untrusted). Called only when
// at least one side is present; the store validates both-or-neither, so a legal
// probe carries both — the "—" fallback only guards drifted data.
function predictionPair(p) {
  return `
    <div class="prediction-pair">
      <div class="pred if-true"><span class="pred-label">if true</span><span class="pred-text">${esc(
        p.expected_if_true || "—"
      )}</span></div>
      <div class="pred if-false"><span class="pred-label">if false</span><span class="pred-text">${esc(
        p.expected_if_false || "—"
      )}</span></div>
    </div>`;
}

// guardrailValue renders a solved case's guardrail as an inert type badge plus its
// free-text ref (harden-detective-gates D-J). The ref is ALWAYS plain escaped text,
// never a link: the board is GET-only with no external origins, so a guardrail ref
// that happens to look like a URL is displayed, never fetched or made clickable.
function guardrailValue(g) {
  const ref = g.ref ? ` <span class="guardrail-ref">${esc(g.ref)}</span>` : "";
  return `${badge("guardrail", g.type || "")}${ref}`;
}

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

// descriptionPanel shows the full bug description below the title header
// (add-case-titles D3): the detail view is title-header + description. Soft-
// structure heading lines (Symptom:/Expected:/Repro:/Context:) are bolded on
// render (renderDescription). An empty description yields no panel.
function descriptionPanel(sess) {
  const body = renderDescription(sess.description);
  if (!body) return "";
  return `<div class="panel description">${body}</div>`;
}

// confirmedHyp returns the winning hypothesis for the verdict panel: the one the
// resolution names, else any hypothesis marked confirmed on the board. A case with
// neither has no verdict to show (returns null → no panel, D3).
function confirmedHyp(sess, hyps) {
  const r = sess.resolution;
  if (r && r.confirmed_hypothesis_id) {
    const named = hyps.find((h) => h.id === r.confirmed_hypothesis_id);
    if (named) return named;
  }
  return hyps.find((h) => h.status === "confirmed") || null;
}

// confirmingProbes returns the probes attributed to the confirmed hypothesis, for
// the "Confirmed by" fact row — the instrumentation that settled the case.
function confirmingProbes(probes, hypId) {
  return (probes || []).filter((p) => p.hypothesis_id === hypId);
}

// factRow renders one labeled fact in the verdict's label/value grid. value is
// pre-built HTML (chips/escaped text); label is a literal from this file.
function factRow(label, value) {
  return `<div class="fact"><span class="fact-label">${label}</span><span class="fact-value">${value}</span></div>`;
}

// verdictPanel renders the case's climax ABOVE the board when a hypothesis is
// confirmed or a resolution is recorded (polish-case-board D3). The layout reads
// top-down like a closing report: a VERDICT eyebrow with the closed date, the
// confirmed suspect chip, the confirmed statement as a quoted lede (ink text,
// coral rule — never a wall of red), then Root cause / Fix / Confirmed by fact
// rows. Confirmed-by lists the attributed probes' display names and the runs they
// fired in as chips. An open case with no confirmation renders nothing — no empty
// panel. idx supplies the confirmed hypothesis's chip (which carries coral).
function verdictPanel(sess, hyps, probes, runs, idx) {
  const win = confirmedHyp(sess, hyps);
  const r = sess.resolution;
  if (!win && !r) return "";

  const rootCause = (r && r.root_cause) || "";
  const fix = (r && r.fix_summary) || "";

  const probeChips = win
    ? confirmingProbes(probes, win.id)
        .map((p) => `<span class="ev-chip">${esc(displayName(p.id))}</span>`)
        .join("")
    : "";
  // Closed runs carry verdicts; surface them as the runs that produced the proof.
  const runChips = (runs || [])
    .filter((rn) => rn.closed_at)
    .map(
      (rn) =>
        `<span class="ev-chip run">${esc(displayName(rn.id))}${
          rn.verdict ? ` · ${esc(rn.verdict)}` : ""
        }</span>`
    )
    .join("");

  // Prevention references recorded with a solved close (harden-detective-gates
  // D-J): a regression-test ref and/or a guardrail, both shown as inert text — the
  // board never resolves, fetches, or executes them (local-only invariant).
  const regressionRef = (r && r.regression_test_ref) || "";
  const guardrail = r && r.guardrail;

  const facts = html([
    rootCause ? factRow("Root cause", esc(rootCause)) : "",
    fix ? factRow("Fix", esc(fix)) : "",
    probeChips || runChips
      ? factRow(
          "Confirmed by",
          `<span class="ev-chips">${probeChips}${runChips}</span>`
        )
      : "",
    regressionRef ? factRow("Regression test", esc(regressionRef)) : "",
    guardrail ? factRow("Guardrail", guardrailValue(guardrail)) : "",
  ]);

  return `
    <section class="verdict">
      <div class="verdict-top">
        <span class="verdict-label">Verdict</span>
        ${
          r && r.closed_at
            ? `<span class="verdict-when">Closed ${esc(fmtDate(r.closed_at))}</span>`
            : ""
        }
      </div>
      ${win ? `<div class="verdict-suspect">${chipFor(idx, win.id)} ${badge("confirmed", "confirmed")}</div>` : ""}
      ${win ? `<p class="verdict-statement">${esc(cleanStatement(win.statement))}</p>` : ""}
      ${facts ? `<div class="facts">${facts}</div>` : ""}
    </section>`;
}

// radiusVerdict renders a blast-radius hit's verdict as an inert badge
// (add-blast-radius D-D). A hit the agent has not graded carries an empty verdict
// and reads "unreviewed", so a partial review is never mistaken for a clean sweep.
// The store enum (sibling-bug / safe / already-covered) doubles as the badge
// palette class; a drifted value still renders as inert escaped text via badge().
function radiusVerdict(v) {
  const label = v || "unreviewed";
  return badge(label, label);
}

// radiusHitRow renders one daemon-recorded sibling occurrence: its file:line as
// inert monospace (loc → a span, never an anchor — the board is GET-only with no
// external origins, so a hit is displayed, never fetched or linked), the trimmed
// excerpt as escaped code, and the verdict badge with the agent's optional note.
// Every field was recorded by the daemon's search; the UI only displays it.
function radiusHitRow(h) {
  const note = h.note
    ? `<div class="note">${esc(h.note)}</div>`
    : "";
  return `
    <tr>
      <td>${loc(h.file, h.line)}</td>
      <td><code class="radius-excerpt">${esc(h.excerpt || "")}</code></td>
      <td>${radiusVerdict(h.verdict)}${note}</td>
    </tr>`;
}

// RADIUS_PREVIEW is how many hits render before the rest fold behind the
// "Show N more hits" expander. Graded hits always sort into the preview.
const RADIUS_PREVIEW = 12;

// radiusSection renders the blast radius — the daemon-executed sibling-occurrence
// search recorded after a confirm (add-blast-radius D-A/D-C). It shows the agent's
// regex pattern, every recorded hit (file:line + excerpt + verdict badge), the
// unreviewed count (D-D — a partial review must never read as a clean sweep), and a
// visible truncation notice when the hit cap was reached (D-B). The section is
// omitted entirely when the session carries no radius (case-board-ui delta: no
// radius → no section) so legacy and un-mapped cases are unchanged. Everything is
// escaped and inert — the pattern and excerpts are agent/user text.
function radiusSection(sess) {
  const radius = sess.blast_radius;
  if (!radius) return "";
  const hits = radius.hits || [];
  const unreviewed = hits.reduce((n, h) => n + (h.verdict ? 0 : 1), 0);

  const note = radius.note ? `<div class="radius-note">${esc(radius.note)}</div>` : "";
  // Truncation mirrors flood control: the cap is bounded and always disclosed, so a
  // truncated hit list is never mistaken for the complete set (D-B).
  const truncation = radius.truncated
    ? `<div class="radius-truncation">${badge(
        "truncated",
        "truncated"
      )}<span>Hit cap reached — some occurrences are not shown. Narrow the pattern and re-run.</span></div>`
    : "";

  // Long hit lists collapse (a 500-hit truncated search must stay scannable):
  // graded hits surface first — a sibling-bug verdict must never hide below the
  // fold — then the first RADIUS_PREVIEW rows render and the tail folds behind a
  // native <details> expander labeled with the hidden count. No script handler,
  // keyboard-accessible by default, content stays inert escaped text.
  const ordered = hits
    .filter((h) => h.verdict)
    .concat(hits.filter((h) => !h.verdict));
  const preview = ordered.slice(0, RADIUS_PREVIEW);
  const rest = ordered.slice(RADIUS_PREVIEW);
  const hitTable = (rows) =>
    `<table><thead><tr><th>Location</th><th>Excerpt</th><th>Verdict</th></tr></thead><tbody>${rows
      .map(radiusHitRow)
      .join("")}</tbody></table>`;
  const more = rest.length
    ? `<details class="radius-more"><summary>Show ${rest.length} more hit${
        rest.length === 1 ? "" : "s"
      }</summary>${hitTable(rest)}</details>`
    : "";
  const body = hits.length
    ? hitTable(preview) + more
    : `<p class="empty">The search recorded no sibling sites.</p>`;

  return html([
    `<h2>Blast radius (${hits.length})</h2>`,
    `<section class="radius">`,
    `<div class="radius-head">`,
    `<code class="radius-pattern">${esc(radius.pattern || "")}</code>`,
    radius.searched_at
      ? `<span class="radius-meta">searched ${esc(fmtDate(radius.searched_at))}</span>`
      : "",
    `<span class="radius-unreviewed ${unreviewed ? "pending" : ""}">${unreviewed} unreviewed</span>`,
    `</div>`,
    note,
    truncation,
    body,
    `</section>`,
  ]);
}

// suspectPanel renders one active/confirmed hypothesis card: a left-edge color bar
// (its palette color, or coral when confirmed) plus the display name, the cleaned
// statement, and its status badge (polish-case-board D2). meta carries the board
// index (→ color) and is supplied by the caller from hypIndex.
function suspectPanel(h, index) {
  const confirmed = h.status === "confirmed";
  const killed = h.status === "killed";
  // Confirmed owns coral; killed desaturates (muted accent); otherwise palette.
  const color = confirmed ? "var(--coral)" : killed ? "var(--muted)" : hypothesisColor(index);
  const statusLabel = killed ? "ruled out" : h.status;
  return `
    <div class="panel suspect ${confirmed ? "confirmed" : ""} ${
    killed ? "killed" : ""
  }" data-hid="${esc(h.id)}" style="--chip:${esc(color)}">
      <div class="statement">${hypChip(
        h.id,
        color,
        h.status
      )} ${esc(cleanStatement(h.statement))} ${badge(h.status, statusLabel)}</div>
      ${noteLine(h)}
    </div>`;
}

// ruledOutItem renders one killed hypothesis as a muted "ruled out" line beneath
// the active board (polish-case-board D3): the story reads verdict-first, surviving
// suspects next, eliminated ones receding at the bottom.
function ruledOutItem(h, index) {
  return `
    <div class="ruled-out" data-hid="${esc(h.id)}" style="--chip:${esc(
    hypothesisColor(index)
  )}">
      ${hypChip(h.id, hypothesisColor(index), "killed")}
      <span class="ro-statement">${esc(cleanStatement(h.statement))}</span>
      ${badge("killed", "ruled out")}
      ${noteLine(h)}
    </div>`;
}

// probeRow renders a probe's registry line. A probe carrying a discriminating
// prediction pair (harden-detective-gates D-A) emits a second, full-width row
// beneath it so if-true/if-false read paired under the probe; a prediction-less
// probe (path tracer) renders the single row unchanged. The colspan matches the
// five probes-table columns (Location, Probe, Hypothesis, Status, Note).
function probeRow(p, idx) {
  const main = `
    <tr>
      <td>${loc(p.file, p.line)}</td>
      <td>${esc(displayName(p.id))}</td>
      <td>${chipFor(idx, p.hypothesis_id)}</td>
      <td>${p.removed ? badge("closed", "removed") : badge("open", "live")}</td>
      <td>${esc(cleanStatement(p.note) || "")}</td>
    </tr>`;
  const predicted = p.expected_if_true || p.expected_if_false;
  const pred = predicted
    ? `<tr class="probe-prediction"><td colspan="5">${predictionPair(p)}</td></tr>`
    : "";
  return main + pred;
}

// runRow renders a run's timeline line. A run carrying a recorded fix prediction
// (harden-detective-gates D-D — stamped before the evidence returned) emits a
// second, full-width row showing it, so the run detail reads the observed outcome
// against the recorded claim. Prediction-less runs render the single row unchanged.
// The colspan matches the four runs-table columns (Run, Verdict, Started, Closed).
function runRow(r) {
  const status = r.closed_at
    ? badge("verdict", r.verdict || "closed")
    : badge("open", "open");
  const main = `
    <tr>
      <td>${esc(displayName(r.id))}</td>
      <td>${status}</td>
      <td>${fmtDate(r.started_at)}</td>
      <td>${r.closed_at ? fmtDate(r.closed_at) : "—"}</td>
    </tr>`;
  const pred = r.prediction
    ? `<tr class="run-prediction"><td colspan="4"><span class="pred-label">prediction</span> <span class="pred-text">${esc(
        r.prediction
      )}</span></td></tr>`
    : "";
  return main + pred;
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
  tail.innerHTML = rows
    .map((r) => tailRow(r.ts, displayName(r.probe), r.body, "", false, r.truncated))
    .join("");
  tail.scrollTop = tail.scrollHeight;
}

// startStream subscribes to the session's SSE feed and appends incoming events to
// the tail (case-board-ui spec: watching a reproduction live). The board section
// is re-rendered on a board event so hypothesis status updates appear without a
// reload. EventSource reconnects natively; closeStream() releases the subscriber.
function startStream(view, tail, dot, sess, indexOf) {
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
    append(ev.ts, displayName(ev.probe), eventBody(ev), "");
  });
  source.addEventListener("run", (e) => {
    const run = JSON.parse(e.data).payload || {};
    append(
      run.started_at || new Date().toISOString(),
      displayName(run.id),
      run.closed_at ? `run closed: ${esc(run.verdict || "")}` : "run opened",
      "run"
    );
  });
  source.addEventListener("probe", (e) => {
    const p = JSON.parse(e.data).payload || {};
    append(new Date().toISOString(), displayName(p.id), `probe ${p.removed ? "removed" : "registered"} @ ${esc(p.file)}:${esc(p.line)}`, "probe");
  });
  source.addEventListener("board", (e) => {
    const h = JSON.parse(e.data).payload || {};
    append(
      new Date().toISOString(),
      displayName(h.id),
      `${esc(cleanStatement(h.statement) || "")} → ${esc(h.status || "")}`,
      "board"
    );
    // Reflect the status change in the suspect board in place. Re-render with the
    // hypothesis's stable board index so its palette color never shifts mid-session;
    // an unknown id (newly streamed) falls back to position 0.
    const panel = view.querySelector(`.suspect[data-hid="${CSS.escape(h.id)}"]`);
    if (panel) panel.outerHTML = suspectPanel(h, indexOf(h.id));
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

  // The board index (id → position) fixes each hypothesis's palette color for the
  // whole view; every chip resolves through it (polish-case-board D2).
  const idx = hypIndex(hyps);
  const indexOf = (id) => {
    const meta = idx.get(id);
    return meta ? meta.index : 0;
  };

  // The active board carries surviving suspects (active + confirmed). Killed
  // hypotheses recede into a muted "ruled out" list below it (D3) so the story
  // reads verdict → survivors → eliminated, top-down.
  const active = hyps.filter((h) => h.status !== "killed");
  const ruledOut = hyps.filter((h) => h.status === "killed");

  view.innerHTML = html([
    caseHeader(sess),
    `<div class="crumbs">#${esc(sess.id)} · ${esc(sess.cwd || "?")} · opened ${fmtDate(
      sess.created_at
    )} · ${open ? badge("open", "open") : badge("closed", "closed")}</div>`,
    caseTabs(sess.id, "detail"),
    descriptionPanel(sess),

    // Verdict panel leads when the case is solved; renders nothing on an open,
    // unconfirmed case (D3 — no empty panel).
    verdictPanel(sess, hyps, probes, runs, idx),

    // Blast radius follows the verdict — the "where else does this bug live" map
    // recorded after the confirm (add-blast-radius). Omitted when there is no
    // radius, so open/legacy cases are unchanged.
    radiusSection(sess),

    `<h2>Suspects (${hyps.length})</h2>`,
    active.length
      ? active.map((h) => suspectPanel(h, indexOf(h.id))).join("")
      : `<p class="empty">No suspects on the board yet.</p>`,
    ruledOut.length
      ? `<div class="ruled-out-list"><div class="section-label">Ruled out (${ruledOut.length})</div>${ruledOut
          .map((h) => ruledOutItem(h, indexOf(h.id)))
          .join("")}</div>`
      : "",

    `<h2>Probes (${probes.length})</h2>`,
    probes.length
      ? `<table><thead><tr><th>Location</th><th>Probe</th><th>Hypothesis</th><th>Status</th><th>Note</th></tr></thead><tbody>${probes
          .map((p) => probeRow(p, idx))
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
    startStream(view, tail, dot, sess, indexOf);
  }
}
