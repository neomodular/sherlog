# Tasks: add-case-board-ui

## 1. Store: resolutions, recall, diff

- [x] 1.1 Add `Resolution` to Session (root cause, fix summary, confirmed hypothesis id, closed-at); persist in state.json; nil-safe load of old files; close-with-resolution store API
- [x] 1.2 Implement keyword recall: tokenize/stopword-strip, TF overlap scoring over closed sessions (description + root cause + confirmed hypothesis), top-3 with min threshold
- [x] 1.3 Implement run diff in store: per-probe fired/counts/first-last samples for two runs, divergence flag (one-sided or ≥10× count ratio), truncation disclosure, same-session validation
- [x] 1.4 In-process pub/sub on the store (log/board/run/probe events) with non-blocking subscriber drop
- [x] 1.5 Unit tests: resolution persistence + old-state compat, recall scoring/threshold/empty, diff correctness + invalid pairs, pub/sub under concurrent publish

## 2. Daemon: new endpoints + SSE

- [x] 2.1 Read endpoints for the UI: session list with resolutions, session detail, stale probes, `GET /api/sessions/<id>/diff?a=&b=`
- [x] 2.2 SSE endpoint `GET /api/events?session=<id>` bridging store pub/sub to `text/event-stream`, with per-subscriber buffering and drop-on-stall
- [x] 2.3 Integration tests: SSE delivery during ingest, subscriber stall drop, diff endpoint, read-only guarantee (UI routes are GET-only)

## 3. MCP + skill

- [x] 3.1 `diff_runs(run_a, run_b)` tool; `debug_end` optional resolution fields (backward compatible); `debug_start` related-cases section
- [x] 3.2 Update `skills/debug/SKILL.md`: record resolutions at close, use recalled cases as cited leads (never evidence), include Case Board URL in the banner
- [x] 3.3 E2E test: solve a simulated case with resolution → new debug_start recalls it → diff_runs across reproduce/fixed-check runs

## 4. Web UI

- [x] 4.1 Embedded asset pipeline: `internal/daemon/ui/` with go:embed, single page + vanilla JS modules, hash navigation, navy/coral theme + mascot header, no external requests
- [x] 4.2 Cases view (open first, closed with resolution one-liners) and case detail view (board, probes, runs, evidence list)
- [x] 4.3 Live evidence tail wired to SSE (EventSource with native reconnect), flood-truncation badges
- [x] 4.4 Run comparison view: two-run picker, side-by-side per-probe columns, divergent probes pinned
- [x] 4.5 Stale probes view
- [ ] 4.6 Manual cross-browser pass (Chromium + Firefox) against a live investigation; record findings in examples/DOGFOOD.md

## 5. Docs touch

- [x] 5.1 README: Case Board section with screenshot placeholder, URL, read-only note; security notes updated (local browser visibility = same boundary as ~/.sherlog)
