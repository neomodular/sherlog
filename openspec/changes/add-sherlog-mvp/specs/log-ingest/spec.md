# log-ingest

## ADDED Requirements

### Requirement: Localhost HTTP log ingestion
The daemon SHALL accept log events via `POST /log/<session-id>/<probe-id>` on `127.0.0.1:2218` (or `SHERLOG_PORT` override) and SHALL bind to the loopback interface only.

#### Scenario: Probe posts a JSON body
- **WHEN** a probe POSTs `{"token": null, "retries": 3}` to `/log/a3f9/p2` for an open session `a3f9` with registered probe `p2`
- **THEN** the daemon stores an event with parsed JSON body, probe `p2`, the current run (if one is open), and a server-assigned monotonic timestamp, and responds `200` in under 50ms

#### Scenario: Daemon bound to loopback only
- **WHEN** a request is sent to the daemon's port on a non-loopback interface
- **THEN** the connection is refused because the daemon listens on `127.0.0.1` only

### Requirement: Tolerant body parsing
The daemon SHALL ignore the request `Content-Type` header, SHALL attempt to parse every body as JSON, and SHALL store unparseable bodies as raw strings. The endpoint SHALL respond `200` to any well-routed request regardless of body content, including empty bodies.

#### Scenario: Non-JSON body accepted
- **WHEN** a probe POSTs the body `user=42 state=dirty` with no Content-Type
- **THEN** the daemon stores the event with the raw string body and responds `200`

#### Scenario: Empty body accepted
- **WHEN** a probe POSTs an empty body (a bare "probe reached" signal)
- **THEN** the daemon stores the event with an empty body and responds `200`

### Requirement: Browser-safe CORS behavior
The daemon SHALL serve ingest responses with permissive CORS headers (`Access-Control-Allow-Origin: *`) and SHALL answer `OPTIONS` preflight requests successfully, so probes work from browser JavaScript. Documented probe forms SHALL avoid preflight by never setting a JSON Content-Type.

#### Scenario: Browser fetch without preflight
- **WHEN** browser JS executes `fetch("http://127.0.0.1:2218/log/a3f9/p1", {method: "POST", body: JSON.stringify({x: 1})})` from any origin
- **THEN** the request is a CORS simple request (no preflight), the daemon responds `200` with `Access-Control-Allow-Origin: *`, and the event is stored with parsed JSON

#### Scenario: Preflight answered anyway
- **WHEN** a client sends `OPTIONS /log/a3f9/p1` with CORS preflight headers
- **THEN** the daemon responds with headers permitting the POST to proceed

### Requirement: Unknown-session events are dropped
The daemon SHALL drop events addressed to session IDs that do not correspond to an open session, responding `200` without storing anything, to neutralize drive-by localhost POSTs.

#### Scenario: Drive-by POST to a guessed path
- **WHEN** a request POSTs to `/log/zzzz/p1` and no session `zzzz` exists
- **THEN** the daemon responds `200` and stores nothing

### Requirement: Per-probe flood control
The daemon SHALL bound storage per probe per run to the first N and last N events (default N=20) plus an exact total counter, dropping middle events once the bound is exceeded.

#### Scenario: Probe in a hot loop
- **WHEN** probe `p2` fires 48,201 times during one run
- **THEN** the daemon retains the first 20 and last 20 events, records a total count of 48,201, and discards the rest

### Requirement: Health endpoint
The daemon SHALL expose `GET /health` returning `200` with version and uptime, usable by the MCP process and the skill's connectivity check.

#### Scenario: Health check
- **WHEN** a client GETs `/health` while the daemon is running
- **THEN** it receives `200` with a JSON body containing the daemon version
