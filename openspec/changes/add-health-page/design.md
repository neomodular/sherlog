# Design: add-health-page

## Context

The Case Board (vanilla JS, hash navigation, embedded via go:embed) ships with Cases, Case detail, and Stale Probes views. `/health` returns JSON {version, uptime, config}. This change adds a fourth view backed by one new aggregation endpoint.

## Goals / Non-Goals

**Goals:**
- One-glance answer to "is sherlog healthy and what is it doing", in the existing UI, matching its theme.
- Diagnosability: config-with-sources, data location, activity recency — the things support conversations always need.
- Zero breakage of the machine-readable `/health`.

**Non-Goals:**
- Historical metrics, charts, or time series (a counter window of one hour, no storage of history).
- Prometheus/OpenMetrics export (revisit on demand).
- Remote/aggregated monitoring — loopback page only.

## Decisions

### D1: Separate `/api/stats`, `/health` untouched
`/health` is a stability contract (scripts, MCP spawn checks, docs). Stats aggregation has different freshness and cost; keeping it separate means `/health` stays O(1) and frozen. `/api/stats` returns one JSON document the view renders directly.

### D2: Counters live in the store; gauges in the daemon
Store owns durable-ish facts it already guards with its lock: total events, last-event time, per-hour ingest (ring of 60 minute-buckets, memory-only, zeroed on restart — documented as "since daemon start"). Daemon owns process facts: SSE subscriber gauge (atomic int), PID, started-at, listener address, self-checks. Disk usage walks `~/.sherlog` on request with a 5s cache to keep the endpoint cheap under refresh.

### D3: Self-checks are boolean probes with reasons
`storage_writable` (create+delete a temp file in the data dir) and `loopback_only` (listener host is 127.0.0.1). Each reports {ok, detail}. The view's header derives from them: all ok → mascot + "on the case"; any failure → plain text of which check failed and the detail. No yellow states in v1.

### D4: View refresh = poll, not SSE
Health data has no event source; the view polls `/api/stats` every 5s while visible (and pauses on hidden tab via the Page Visibility API). Uptime ticks client-side between polls. Reuses the existing fetch/render helpers; no new patterns.

## Risks / Trade-offs

- [Disk walk slow on huge ~/.sherlog] → 5s cache + size computed from session dirs only; retention (add-config) is the real fix for bloat.
- [Hourly window resets on restart] → labeled "since start"; acceptable for a local tool.
- [Header flicker between polls] → render only on data change (compare JSON), uptime ticks independently.

## Migration Plan

Pure addition. Rollback = revert; no persisted formats touched.

## Open Questions

None.
