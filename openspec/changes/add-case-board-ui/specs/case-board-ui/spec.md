# case-board-ui

## ADDED Requirements

### Requirement: Case Board served by the daemon
The daemon SHALL serve a browser UI at `GET /` on its existing listener, with all assets embedded in the binary (`go:embed`) and zero requests to external origins.

#### Scenario: Open the Case Board
- **WHEN** a user browses to `http://127.0.0.1:2218/` while the daemon runs
- **THEN** the Case Board loads fully from embedded assets with no external network requests

### Requirement: Case list and case detail views
The UI SHALL list all sessions (open first, then closed with their resolution summaries) and SHALL show per-case detail: bug description, hypothesis board (statement, status, evidence notes), probe registry (id, file:line, linked hypothesis, removed flag), and run history (verdicts, per-probe counts).

#### Scenario: Browsing a closed case
- **WHEN** the user opens a closed case that recorded a resolution
- **THEN** the detail view shows the root cause, fix summary, confirmed hypothesis, and the full board/probe/run history as the investigation left them

### Requirement: Live evidence tail
The UI SHALL stream new events for an open session via SSE (`/api/events?session=<id>`) — log events, board changes, run open/close, probe changes — appearing in the case detail without reload, honoring flood-control truncation.

#### Scenario: Watching a reproduction live
- **WHEN** the user has the case open in a browser while probes fire during a run
- **THEN** events appear in the evidence tail as they arrive and the board reflects hypothesis updates without a page reload

#### Scenario: Slow browser does not block the daemon
- **WHEN** an SSE subscriber stops reading (full buffer or closed connection)
- **THEN** the daemon drops that subscriber without delaying ingest or other subscribers

### Requirement: Run comparison view
The UI SHALL provide a side-by-side comparison of any two runs of a case using the run-diff data, pinning divergent probes (fired in only one run, or large count divergence) to the top and badging flood-truncated probes.

#### Scenario: Comparing failing vs fixed-check runs
- **WHEN** the user selects run 1 (reproduced) and run 3 (fixed-check) in the comparison picker
- **THEN** probes are shown side by side with counts and sample values from each run, divergent probes first

### Requirement: Stale probes view
The UI SHALL show all registered-but-not-removed probes across all sessions with session, file, and line — the browser equivalent of `sherlog probes --stale`.

#### Scenario: Spotting leftovers
- **WHEN** any session (open or closed) has probes not marked removed
- **THEN** the stale probes view lists each with enough information to locate and delete the probe line

### Requirement: Read-only UI
Browser-facing routes SHALL be read-only; no UI interaction SHALL mutate sessions, hypotheses, probes, runs, or logs. Mutations remain exclusive to the MCP/internal API path.

#### Scenario: No mutation surface
- **WHEN** the UI is exercised end to end
- **THEN** every request it issues is a GET (page, data, SSE) and daemon state is byte-identical afterward
