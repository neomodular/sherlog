# Design: add-case-board-ui

## Context

The MVP daemon (see archived design of `add-sherlog-mvp`, decisions D1–D14) owns an HTTP server on 127.0.0.1:2218 with ingest (`/log/...`), health, and internal API (`/api/...`) routes. Store holds sessions/hypotheses/probes/runs in memory with JSON/JSONL persistence. This change adds a browser UI on the same listener plus three data capabilities it showcases: resolutions, recall, and run diffs.

```
 browser ──GET /──────────▶ embedded SPA (go:embed, vanilla JS)
 browser ──GET /api/...───▶ existing + new read endpoints
 browser ──GET /api/events▶ SSE: live log/board updates
 Claude ───MCP────────────▶ mutations (unchanged path) + diff_runs
```

## Goals / Non-Goals

**Goals:**
- Zero-install observability: open a URL, see everything the daemon knows.
- Live evidence tail during reproduction (the "watch the detective work" moment).
- Closed cases become useful: browsable archive + automatic recall at `debug_start`.
- `diff_runs` as both MCP tool and visual comparison.

**Non-Goals:**
- Any mutation from the browser (no close/delete/edit — MCP only).
- Authentication/multi-user (loopback trust boundary, same as `~/.sherlog` files).
- JS frameworks, bundlers, or build steps; no CDN/external requests.
- Embedding-based semantic search (keyword scoring only — local, zero deps).

## Decisions

### D1: UI stack — vanilla JS + SSE, embedded via go:embed
Single static page + small JS modules served from `internal/daemon/ui/` with `go:embed`. SSE (`text/event-stream`) over WebSockets: it's one-directional (server→browser), works with plain `http.Flusher`, auto-reconnects natively via `EventSource`, and adds no dependency. No build step keeps contributor friction at zero and the binary self-contained.

### D2: Read-only enforced at the route layer
Browser-facing routes are GET-only; all mutating endpoints remain under the internal API used by the MCP process. The UI never gains a write path, so the review surface for "can a malicious local page mutate state" stays where it already was (and unknown-session drops still apply to ingest).

### D3: SSE event model
One stream, `/api/events?session=<id>`, emitting typed events: `log` (new event, flood-control aware), `board` (hypothesis change), `run` (open/close), `probe` (register/remove). The store gets a lightweight in-process pub/sub (subscriber channels guarded by the existing lock discipline); SSE handlers subscribe, the MCP/inges paths publish. Dropped subscribers (slow browsers) are disconnected rather than blocking publishers.

### D4: Resolution record
`Session` gains an optional `Resolution {RootCause, FixSummary, ConfirmedHypothesisID, ClosedAt}` persisted in `state.json`. `debug_end` accepts the new optional fields; empty stays valid (a case can close unsolved — recorded as such). The skill is updated to always supply them when a root cause was confirmed.

### D5: Recall — keyword scoring, no embeddings
At `debug_start`, tokenize the new bug description; score closed sessions by weighted overlap against their bug description + root cause + confirmed hypothesis statement (simple TF, lowercase, stopword-stripped). Return top 3 above a minimum score with id, description, root cause, and fix summary. Deliberately dumb: local, fast, zero deps, explainable. Revisit only if real usage shows misses.

### D6: diff_runs semantics
Input: two run IDs of the same session. Output per registered probe: fired-in-A/fired-in-B, counts, first/last sample bodies from each side, plus a `divergent` flag (fired in exactly one, or count ratio beyond 10×). Computed in the store from already-retained flood-controlled events — no new storage. Exposed as `GET /api/sessions/<id>/diff?a=<run>&b=<run>` and MCP tool `diff_runs(run_a, run_b)`. The UI renders it as side-by-side columns with divergent probes pinned to the top.

### D7: UI information architecture
Three views, no router library (hash navigation): **Cases** (open first, then closed with resolution one-liners), **Case detail** (suspect board, probes, run timeline, evidence tail with live SSE when a run is open, "compare runs" picker), **Stale probes** (the `probes --stale` data with session/file/line). Detective theming consistent with the brand: mascot sprite in the header, status vocabulary, navy/coral palette.

## Risks / Trade-offs

- [SSE subscriber leaks if browsers linger] → per-subscriber buffered channel; on full buffer or closed request context, unsubscribe and drop.
- [Recall returns misleading matches] → minimum-score threshold + always labeled "possibly related past cases"; the skill treats them as leads, never evidence.
- [Diff misleads when flood control dropped middles] → diff output carries the same truncation disclosures as queries; UI badges truncated probes.
- [Embedded assets bloat or staleness] → assets are plain files in-repo; CI builds embed them automatically; no versioning needed beyond the binary's.
- [Local browser users see investigation data] → identical trust boundary to `~/.sherlog` file access; documented in README security notes.

## Migration Plan

Backward-compatible additions: new optional MCP fields/tools, new GET routes, additive `state.json` field (older state files load with nil Resolution). Existing sessions display without resolutions. No data migration. Rollback = revert; persisted Resolution fields are ignored by older binaries.

## Open Questions

None.
