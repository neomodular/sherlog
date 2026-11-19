# log-ingest (delta)

## ADDED Requirements

### Requirement: Stats aggregation endpoint
The daemon SHALL serve `GET /api/stats` returning: vitals (version, port, PID, started-at), effective config with sources, storage (data directory path, disk usage bytes with ≤5s staleness, open/closed session counts, total events, field-notes count), activity (last-event time, events in the trailing hour since daemon start, active SSE subscriber count, open run session/id if any), stale-probe count, and self-checks (`storage_writable`, `loopback_only`, each {ok, detail}). The existing `/health` response SHALL remain byte-compatible.

#### Scenario: Stats reflect activity
- **WHEN** a session is open with an open run and probes fired 14 events in the last hour
- **THEN** /api/stats reports the open run, last-event time, and an hourly count of 14

#### Scenario: /health contract preserved
- **WHEN** existing consumers GET /health after this change
- **THEN** the response shape is unchanged

#### Scenario: Self-check failure is reported, not hidden
- **WHEN** the data directory is not writable
- **THEN** /api/stats reports storage_writable {ok:false} with a detail message while the endpoint itself still returns 200
