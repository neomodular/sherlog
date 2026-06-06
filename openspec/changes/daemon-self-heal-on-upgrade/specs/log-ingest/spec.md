# log-ingest (delta)

## ADDED Requirements

### Requirement: Graceful shutdown endpoint
The daemon SHALL serve `POST /api/shutdown` on its internal API surface. The handler SHALL respond `200` with `{"ok":true}` before initiating shutdown, then SHALL stop the HTTP server gracefully with a bounded drain (~2 s) followed by a hard close so held long-poll connections (`await_run`) cannot stall the exit; the process SHALL then exit cleanly (exit code 0). The Case Board UI SHALL remain strictly read-only — no board view or script invokes this endpoint.

#### Scenario: Shutdown acknowledged then executed
- **WHEN** a client POSTs /api/shutdown to a running daemon
- **THEN** it receives 200 {"ok":true}, and within the drain budget the port stops accepting connections and the daemon process exits with code 0

#### Scenario: Long-poll cannot stall shutdown
- **WHEN** /api/shutdown arrives while another client is blocked in a long await_run
- **THEN** the daemon exits within the bounded drain window anyway, cutting the held connection; persisted session state is intact on restart

#### Scenario: Method discipline
- **WHEN** a client sends GET /api/shutdown
- **THEN** the daemon refuses it (method not allowed) and keeps running
