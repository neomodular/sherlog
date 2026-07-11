// diff.js — the Run comparison view: a two-run picker and a side-by-side per-probe
// table sourced from the daemon's run-diff (case-board-ui spec: run comparison;
// design D6). Divergent probes (fired in exactly one run, or ≥10× count ratio) are
// pinned to the top by the daemon and badged here; flood-truncated sides are
// badged too so a partial count is never mistaken for a complete one.

import { api } from "./api.js";
import { esc, badge, html, eventBody, displayName, caseTabs } from "./render.js";

// runLabel describes a run for the picker: display name + verdict, so a user can
// tell the reproduced run from the fixed-check run without cross-referencing.
function runLabel(run) {
  const verdict = run.verdict ? ` (${run.verdict})` : run.closed_at ? "" : " (open)";
  return `${displayName(run.id)}${verdict}`;
}

// predictionBlock renders the recorded fix predictions for the two compared runs
// ABOVE the probe divergence list (harden-detective-gates D-D / case-board-ui): a
// fixed-check run carries a prediction stamped before its evidence returned, so the
// divergence below is read against the recorded claim rather than conversation
// memory. Reproduce runs typically carry none; when neither side has one, nothing
// renders. Labeled with each run's display name.
function predictionBlock(diff, aId, bId) {
  const line = (id, text) =>
    `<div class="pred-line"><span class="pred-run">${esc(displayName(id))} predicted</span> <span class="pred-text">${esc(
      text
    )}</span></div>`;
  const rows = html([
    diff.prediction_a ? line(aId, diff.prediction_a) : "",
    diff.prediction_b ? line(bId, diff.prediction_b) : "",
  ]);
  return rows ? `<div class="diff-prediction">${rows}</div>` : "";
}

// sideCell renders one run's column for a probe: count with adoption/truncation
// disclosure and the first/last retained sample bodies (design D6).
function sideCell(side) {
  if (!side || !side.fired) {
    return `<td class="muted">did not fire</td>`;
  }
  const flags = html([
    side.truncated ? badge("truncated", "truncated") : "",
    side.adopted ? badge("adopted", `${side.adopted} adopted`) : "",
  ]);
  const first = side.first ? `<div class="sample">first: ${eventBody(side.first)}</div>` : "";
  const last =
    side.last && side.last !== side.first
      ? `<div class="sample">last: ${eventBody(side.last)}</div>`
      : "";
  return `<td><b>${esc(side.total)}</b> ${flags}${first}${last}</td>`;
}

function diffRow(pd) {
  return `
    <tr class="${pd.divergent ? "divergent" : ""}">
      <td>${esc(displayName(pd.probe))} ${
    pd.divergent ? badge("divergent", "divergent") : ""
  }</td>
      ${sideCell(pd.a)}
      ${sideCell(pd.b)}
    </tr>`;
}

// renderDiff draws the picker for sess and, when a and b are chosen, the diff
// table. The picker is itself read-only: changing a selection updates the hash so
// navigation (and the browser back button) drive the comparison.
export async function renderDiff(view, sess, a, b) {
  const runs = sess.runs || [];
  if (runs.length < 2) {
    view.innerHTML = html([
      caseHeader(sess),
      caseTabs(sess.id, "diff"),
      `<p class="empty">Run comparison needs at least two runs; this case has ${runs.length}.</p>`,
    ]);
    return;
  }
  // Default to the first and last run so the common "reproduced vs fixed-check"
  // comparison is one click away.
  const selA = a || runs[0].id;
  const selB = b || runs[runs.length - 1].id;

  const options = (selected) =>
    runs
      .map(
        (r) =>
          `<option value="${esc(r.id)}" ${r.id === selected ? "selected" : ""}>${esc(
            runLabel(r)
          )}</option>`
      )
      .join("");

  view.innerHTML = html([
    caseHeader(sess),
    caseTabs(sess.id, "diff"),
    `<div class="picker">
       <label>Run A <select id="diffA">${options(selA)}</select></label>
       <span class="muted">vs</span>
       <label>Run B <select id="diffB">${options(selB)}</select></label>
     </div>`,
    `<div id="diffBody"><p class="loading">Comparing…</p></div>`,
  ]);

  const selectA = view.querySelector("#diffA");
  const selectB = view.querySelector("#diffB");
  const onChange = () => {
    location.hash = `#/case/${sess.id}/diff/${selectA.value}/${selectB.value}`;
  };
  selectA.addEventListener("change", onChange);
  selectB.addEventListener("change", onChange);

  const body = view.querySelector("#diffBody");
  if (selA === selB) {
    body.innerHTML = `<p class="empty">Pick two different runs to compare.</p>`;
    return;
  }
  let diff;
  try {
    diff = await api.diff(sess.id, selA, selB);
  } catch (e) {
    body.innerHTML = `<p class="error">${esc(e.message)}</p>`;
    return;
  }
  if (!diff.probes || diff.probes.length === 0) {
    body.innerHTML = `<p class="empty">No probes fired in either run.</p>`;
    return;
  }
  body.innerHTML = html([
    // The recorded fix prediction(s) sit above the divergence list so the observed
    // contrast is judged against the claim (harden-detective-gates D-D).
    predictionBlock(diff, selA, selB),
    `<table>
      <thead><tr><th>Probe</th><th>${esc(displayName(selA))}</th><th>${esc(displayName(selB))}</th></tr></thead>
      <tbody>${diff.probes.map(diffRow).join("")}</tbody>
    </table>`,
  ]);
}

// shortCommit abbreviates a pinned commit SHA for the header (harden-detective-gates
// D-H): the first 8 hex chars, git's conventional short form. A shorter/blank value
// passes through unchanged.
function shortCommit(sha) {
  const s = String(sha || "");
  return s.length > 8 ? s.slice(0, 8) : s;
}

// reproSignal formats the computed repro rate as raw counts — "reproduced 2/3"
// (harden-detective-gates D-I / case-board-ui): a determinism signal shown ONLY once
// at least one repro-attempt run has closed (denominator ≥ 1), never asserted. An
// empty denominator (no closed repro-attempt run yet, or fixed-check-only) yields no
// signal.
function reproSignal(rr) {
  if (!rr) return "";
  const reproduced = rr.reproduced || 0;
  const denom = reproduced + (rr.not_reproduced || 0);
  if (denom < 1) return "";
  return `reproduced ${reproduced}/${denom}`;
}

// headerMeta renders the case's determinism signal and pinned commit beneath the
// title (harden-detective-gates: the case header shows repro rate and pinned commit).
// Both are optional; when neither is present the meta line is omitted.
function headerMeta(sess) {
  const repro = reproSignal(sess.repro_rate);
  const parts = html([
    repro ? `<span class="repro">${esc(repro)}</span>` : "",
    sess.commit ? `<span class="commit mono">${esc(shortCommit(sess.commit))}</span>` : "",
  ]);
  return parts ? `<div class="case-meta">${parts}</div>` : "";
}

// caseHeader is the breadcrumb + title shared by detail and diff views. The
// heading is the case title (the scannable identity), not the description
// (add-case-titles: detail shows the title as the header). The daemon always sends
// a non-empty title (real or derived), so the title field is authoritative here. The
// repro-rate + commit meta line rides under the title (harden-detective-gates).
function caseHeader(sess) {
  return `
    <div class="crumbs"><a href="#/cases">Cases</a> › #${esc(sess.id)}</div>
    <h1>${esc(sess.title || "(untitled case)")}</h1>
    ${headerMeta(sess)}`;
}

export { caseHeader };
