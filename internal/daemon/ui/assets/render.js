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

// --- hypothesis / probe display naming + color identity (polish-case-board D1/D2) ---

// displayName maps a store ID to its human label: `h<N>`→"Hypothesis N",
// `p<N>`→"Probe N" (polish-case-board D1). Any other shape (a run id like `r2`, an
// already-clean string, "—") passes through unchanged so the helper is safe to call
// on every reference. Stored IDs stay `h1`/`p2`; this is presentation only.
export function displayName(id) {
  if (id === null || id === undefined) return "";
  const s = String(id);
  const m = s.match(/^h(\d+)$/);
  if (m) return `Hypothesis ${m[1]}`;
  const p = s.match(/^p(\d+)$/);
  if (p) return `Probe ${p[1]}`;
  return s;
}

// selfPrefix matches a leading self-identifier on a stored statement/note —
// "h1: ", "p2 - ", "h3 – " — that legacy sessions wrote before the skill's
// no-prefix rule (polish-case-board D1/D5). The skill now stores bare claims, but
// older data is untouched on disk, so the UI strips one such prefix at render time.
const selfPrefix = /^[hp]\d+\s*[:\-–]\s*/;

// cleanStatement defensively removes a leading self-ID prefix from a stored
// statement so a derived name is never rendered next to a duplicate raw ID
// ("Hypothesis 1 — h1: race"). Stored data is never modified; this is render-time
// only. References to *other* entities mid-text (e.g. "p3 fired only in run 2") are
// left intact for callers that upgrade them to display names where they choose.
export function cleanStatement(text) {
  if (!text) return "";
  return String(text).replace(selfPrefix, "");
}

// HYPOTHESIS_PALETTE is a six-color categorical palette derived from the
// Okabe–Ito colorblind-safe set and tuned for the board's light "aged paper"
// surface (polish-case-board D2). Colors are assigned by hypothesis index and
// cycle past six. They are a board affordance only — coral stays reserved for the
// confirmed verdict, so it is deliberately absent here.
const HYPOTHESIS_PALETTE = [
  "#0072b2", // blue
  "#009e73", // bluish green
  "#cc79a7", // reddish purple
  "#56b4e9", // sky blue
  "#d55e00", // vermillion
  "#9467bd", // muted violet
];

// hypothesisColor returns the palette color for a hypothesis at a given index
// (its position on the board), cycling for indices past the palette length. A
// negative/unknown index falls back to the first color so a chip is never blank.
export function hypothesisColor(index) {
  if (!Number.isFinite(index) || index < 0) return HYPOTHESIS_PALETTE[0];
  return HYPOTHESIS_PALETTE[index % HYPOTHESIS_PALETTE.length];
}

// hypChip renders a hypothesis reference as a colored dot + display name — the
// single way a hypothesis is named anywhere it appears (probes table, evidence,
// verdict). Color ALWAYS pairs with the name (D2: color never carries meaning
// alone). State overrides the palette: a confirmed hypothesis carries the coral
// accent (the verdict owns coral); a killed one renders muted, signalling "ruled
// out" without a separate word. `color` is a hex string from hypothesisColor; both
// the id and color are caller-supplied, so the dot color is set via a CSS custom
// property (--chip) rather than inline style to keep the value escaped.
export function hypChip(id, color, status) {
  const cls =
    status === "confirmed" ? "confirmed" : status === "killed" ? "killed" : "";
  return `<span class="hyp-chip ${cls}" style="--chip:${esc(color)}"><span class="hyp-dot"></span>${esc(
    displayName(id)
  )}</span>`;
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
