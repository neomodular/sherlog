// api.js — thin read-only client for the daemon's browser-facing endpoints
// (design D2: GET only). Every call is same-origin against 127.0.0.1; no external
// hosts are ever contacted (case-board-ui spec: zero external requests).

async function getJSON(path) {
  const res = await fetch(path, { headers: { Accept: "application/json" } });
  if (!res.ok) {
    let detail = res.statusText;
    try {
      const body = await res.json();
      if (body && body.error) detail = body.error;
    } catch {
      // Non-JSON error body: keep the status text.
    }
    throw new Error(`${res.status} ${detail}`);
  }
  return res.json();
}

export const api = {
  cases: () => getJSON("/api/cases"),
  session: (id) => getJSON(`/api/sessions/${encodeURIComponent(id)}`),
  staleProbes: () => getJSON("/api/probes/stale"),
  diff: (id, a, b) =>
    getJSON(
      `/api/sessions/${encodeURIComponent(id)}/diff?a=${encodeURIComponent(a)}&b=${encodeURIComponent(b)}`
    ),
  query: (id, probe, run) => {
    const params = new URLSearchParams();
    if (probe) params.set("probe", probe);
    if (run) params.set("run", run);
    const qs = params.toString();
    return getJSON(`/api/sessions/${encodeURIComponent(id)}/query${qs ? "?" + qs : ""}`);
  },
  // events returns an EventSource for one session's live stream. EventSource
  // reconnects natively, so the UI needs no retry logic (design D1/D3).
  events: (id) => new EventSource(`/api/events?session=${encodeURIComponent(id)}`),
};
