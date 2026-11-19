# Proposal: add-health-page

## Why

`/health` answers "is the daemon alive" for machines, but a human asking "is sherlog healthy, what is it doing, where is my data, why is it behaving this way" has nowhere to look. The Case Board exists now — a proper health view belongs in it.

## What Changes

- New **Health view** in the Case Board (`#/health`): daemon vitals (version, port, PID, started-at, uptime ticking live), effective configuration with sources, storage panel (data directory path, disk usage, session counts open/closed, total events, field-notes count), activity panel (last event received, events in the last hour, active SSE subscribers, currently open run if any), self-checks (storage dir writable, listener loopback-bound), and a stale-probes count linking to the existing view. Status header with the mascot: "on the case" when healthy, plain failure description per failed check otherwise.
- New `GET /api/stats` endpoint aggregating the above (the JSON `/health` endpoint stays unchanged for machines/scripts).
- Store/daemon counters to back it: ingest totals with hourly window, last-event timestamp, SSE subscriber gauge.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `case-board-ui`: ADDED — the Health view and its requirements.
- `log-ingest`: ADDED — `GET /api/stats` aggregation endpoint and the counters backing it (read-only, loopback, same trust boundary).

## Impact

- Code: `internal/store` (counters, counts), `internal/daemon` (stats endpoint, SSE gauge, self-checks), `internal/daemon/ui/` (health view), docs touch (architecture.md endpoint list, troubleshooting pointer).
- No config, no schema changes, no new dependencies; `/health` consumers unaffected.
