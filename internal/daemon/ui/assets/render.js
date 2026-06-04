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
