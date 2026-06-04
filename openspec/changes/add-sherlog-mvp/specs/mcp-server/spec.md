# mcp-server

## ADDED Requirements

### Requirement: MCP stdio mode in the same binary
The `sherlog` binary SHALL provide an `mcp` subcommand implementing an MCP stdio server, suitable for launch from the plugin's `.mcp.json` with no additional configuration.

#### Scenario: Plugin launches the MCP server
- **WHEN** Claude Code starts the plugin's MCP server via `sherlog mcp`
- **THEN** the process completes the MCP handshake over stdio and lists the sherlog tool set

### Requirement: Daemon auto-spawn
The MCP process SHALL check daemon health on startup and on first tool call, and SHALL spawn the daemon as a detached background process if it is not running, surfacing a clear error only if the port is occupied by a foreign process.

#### Scenario: First use on a fresh machine
- **WHEN** `debug_start` is invoked and no daemon is listening on port 2218
- **THEN** the MCP process spawns `sherlog daemon` detached, waits for `/health`, and proceeds with session creation

#### Scenario: Foreign process owns the port
- **WHEN** the MCP process finds port 2218 answering but not identifying as sherlog
- **THEN** the tool call fails with an error explaining the conflict and the `SHERLOG_PORT` override

### Requirement: Investigation tool surface
The MCP server SHALL expose: `debug_start(bug_description)`, `debug_resume(session_id?)`, `set_hypotheses(hypotheses)`, `update_hypothesis(id, status, note?)`, `register_probe(id, file, line, hypothesis_id, note?)`, `remove_probe(id)`, `await_run(timeout_s?)`, `close_run(verdict)`, `query_logs(filters)`, and `debug_end()`, mapping directly onto daemon session-state and log-query operations.

#### Scenario: debug_start returns the probe contract
- **WHEN** `debug_start` is called with a bug description
- **THEN** the result contains the session ID, the probe URL template, and canonical one-line probe examples for common languages

#### Scenario: debug_end returns the cleanup checklist
- **WHEN** `debug_end` is called
- **THEN** the result lists every probe not marked removed, each with file and line, and the greppable URL fragment for final verification

### Requirement: Long await within MCP constraints
`await_run` SHALL default to a 120-second timeout and SHALL be safely re-invocable so the skill can sustain arbitrarily long reproductions across repeated tool calls without losing the open run.

#### Scenario: Reproduction takes five minutes
- **WHEN** the skill re-invokes `await_run` after consecutive timeouts while the user reproduces a slow bug
- **THEN** each call re-attaches to the same run and the eventual evidence is captured in that single run
