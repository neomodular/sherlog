// render.js — shared DOM/string helpers for the Case Board views (DRY: every view
// formats badges, timestamps, and probe locations the same way). All user/probe
// data is escaped before it reaches the DOM so a malicious probe body can never
// inject markup into the read-only viewer.

// esc HTML-escapes a string for safe interpolation into innerHTML. Probe bodies
// are attacker-influenced (any local page can POST to ingest), so every dynamic
// value passes through here.
export function esc(v) {
  if (v === null || v === undefined) return "";
  return String(v)
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;")
    .replaceAll('"', "&quot;")
    .replaceAll("'", "&#39;");
}

// fmtTime renders an ISO timestamp as a local HH:MM:SS for the dense evidence
// tail; an empty/invalid value yields a dash so rows never show "Invalid Date".
export function fmtTime(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d)) return "—";
  return d.toLocaleTimeString();
}

// fmtDate renders an ISO timestamp as a local date+time for case/run metadata.
export function fmtDate(ts) {
  if (!ts) return "—";
  const d = new Date(ts);
  if (isNaN(d)) return "—";
  return d.toLocaleString();
}

// badge builds a labeled pill; cls selects the palette (board.css). Used for
// statuses, verdicts, and disclosure flags (truncated/adopted/divergent).
export function badge(cls, label) {
  return `<span class="badge ${esc(cls)}">${esc(label)}</span>`;
}

// loc renders a probe's file:line in monospace for quick eyeballing.
export function loc(file, line) {
  return `<span class="loc">${esc(file)}:${esc(line)}</span>`;
}

// eventBody renders a probe hit's payload: parsed JSON is pretty-printed compact,
// a raw string is shown verbatim, an empty body is a muted dash. Always escaped.
export function eventBody(ev) {
  if (ev.body !== undefined && ev.body !== null) {
    try {
      return esc(JSON.stringify(ev.body));
    } catch {
      return esc(String(ev.body));
    }
  }
  if (ev.raw) return esc(ev.raw);
  return '<span class="muted">∅</span>';
}

// el sets a container's HTML from a parts array, filtering out empty pieces so
// callers can conditionally include sections without trailing blanks.
export function html(parts) {
  return parts.filter(Boolean).join("");
}

// descHeading matches a soft-structure heading at the start of a description line:
// one of Symptom/Expected/Repro/Context followed by a colon (add-case-titles D2).
// The skill writes these as plain text; the UI bolds them on render only — there is
// no parser and no stored structure. Anchored to line start so a colon mid-sentence
// is never mistaken for a heading.
const descHeading = /^(Symptom|Expected|Repro|Context):/;

// renderDescription renders a soft-structured description for the detail view
// (add-case-titles D2/D3): every line is HTML-escaped first (probe/user text is
// untrusted), then a leading Symptom:/Expected:/Repro:/Context: label is wrapped in
// <b> — a render-only enhancement over the stored plain text. Lines are joined with
// <br> so the structure is visible without a markdown engine. An empty description
// yields an empty string so callers can omit the block.
export function renderDescription(description) {
  if (!description) return "";
  return description
    .split("\n")
    .map((line) => {
      const safe = esc(line);
      const m = line.match(descHeading);
      // Bold only the heading label (up to and including the colon); the escaped
      // remainder of the line follows in normal weight.
      if (m) {
        const label = esc(m[0]);
        return `<b>${label}</b>${safe.slice(label.length)}`;
      }
      return safe;
    })
    .join("<br>");
}
