# session-state

## ADDED Requirements

### Requirement: Session lifecycle
The daemon SHALL create debug sessions with an unguessable short ID (≥8 random base36 characters), a bug description, the originating working directory, and status `open`; `debug_end` SHALL transition the session to `closed`. Session data SHALL persist under `~/.sherlog/sessions/<id>/`.

#### Scenario: Session created
- **WHEN** `debug_start` is called with bug description "login fails intermittently" from cwd `~/code/app`
- **THEN** a session is created with a random ID, the description, the cwd, status `open`, and a probe URL template `http://127.0.0.1:2218/log/<id>/` is returned

#### Scenario: Concurrent session warning data
- **WHEN** `debug_start` is called and another `open` session exists for the same working directory
- **THEN** the response includes the existing session's ID and description so the caller can warn the user

### Requirement: Hypothesis board persisted in daemon
The daemon SHALL store the session's hypothesis board — each hypothesis having an ID, statement, status (`active`, `killed`, `confirmed`), and evidence notes — and SHALL apply mutations from the MCP tools atomically to `state.json`.

#### Scenario: Hypothesis killed with evidence
- **WHEN** `update_hypothesis(h2, status: killed, note: "p4 showed cache fresh in run 1")` is called
- **THEN** the board persists h2 as `killed` with the note, and subsequent reads return the updated board

### Requirement: Probe registry
The daemon SHALL record every registered probe (ID, file path, line, linked hypothesis, note, `removed` flag) and SHALL report unremoved probes at `debug_end` and via the `sherlog probes --stale` CLI across all sessions.

#### Scenario: Unremoved probes reported at session end
- **WHEN** `debug_end` is called while probes p1 and p3 are registered and only p1 was marked removed
- **THEN** the response lists p3 with its file and line as requiring cleanup

#### Scenario: Stale probes visible weeks later
- **WHEN** `sherlog probes --stale` runs after a session was closed with unremoved probes
- **THEN** those probes are listed with session ID, file, and line

### Requirement: Investigation resume
The daemon SHALL return the complete investigation state (bug description, hypothesis board, probe registry, run history with verdicts) for the most recent open session — or a named session — to power `/debug resume` after context loss.

#### Scenario: Resume after context loss
- **WHEN** `debug_resume()` is called in a fresh Claude session with no arguments
- **THEN** the most recently active open session's full state is returned, sufficient to continue the investigation without any conversational memory

### Requirement: State survives daemon restart
The daemon SHALL recover all session state and log events from disk on startup.

#### Scenario: Restart mid-investigation
- **WHEN** the daemon process is killed and restarted while session a3f9 is open
- **THEN** session a3f9's board, probes, runs, and previously ingested events are intact
