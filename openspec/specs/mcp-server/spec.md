# mcp-server Specification

## Purpose
Expose the investigation to the agent as stateless MCP tools over stdio that route every call to the daemon, starting the daemon on demand.

## Requirements

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

### Requirement: diff_runs tool
The MCP server SHALL expose `diff_runs(run_a, run_b)` returning the daemon's run-diff for the active session as a compact structured payload (divergent probes first), consistent with query-not-dump.

#### Scenario: Claude asks what changed
- **WHEN** `diff_runs(1, 3)` is called after a fix attempt
- **THEN** the result lists divergent probes with per-run counts and samples, enabling root-cause confirmation without raw log reads

### Requirement: Resolution fields on debug_end
`debug_end` SHALL accept optional `root_cause`, `fix_summary`, and `confirmed_hypothesis_id`, recording them as the session's resolution. Existing callers without the new fields SHALL keep working.

#### Scenario: Closing a solved case
- **WHEN** `debug_end` is called with root cause and fix summary after a confirmed fix
- **THEN** the session closes with the resolution recorded and the response still includes the cleanup checklist

### Requirement: Recall in debug_start response
`debug_start` SHALL include the case-recall matches (possibly related closed cases with root causes and fix summaries) in its structured response, as a clearly-labeled advisory section.

#### Scenario: Familiar symptom
- **WHEN** `debug_start` is called and recall finds a similar solved case
- **THEN** the response's related-cases section carries that case's root cause and fix summary alongside the session id and probe template

### Requirement: debug_start accepts a title
`debug_start` SHALL accept an optional `title` parameter alongside the description and pass it to session creation. Callers omitting it SHALL keep working (daemon fallback derivation applies).

#### Scenario: Titled start
- **WHEN** `debug_start(title: "Cart total off by cents", description: "Symptom: ...")` is called
- **THEN** the session is created with that title and the response echoes it

#### Scenario: Legacy caller
- **WHEN** `debug_start` is called with only a description
- **THEN** the call succeeds and the response carries the derived fallback title

### Requirement: Titles in investigation payloads
`debug_resume` SHALL present the title as the case identity (description as detail), and the related-cases section of `debug_start` SHALL identify each recalled case by its title.

#### Scenario: Resume identifies by title
- **WHEN** `debug_resume` returns an investigation
- **THEN** the title appears as the case identity with the full description available separately

### Requirement: map_blast_radius tool
The MCP server SHALL register `map_blast_radius(session_id, pattern, note?)`, passing the pattern to the daemon for execution and returning the recorded radius (hits, truncation flag, unreviewed count). Gate rejections (no confirmed hypothesis, culprit not in hit set, invalid regex) SHALL surface verbatim as actionable tool errors. Existing tools SHALL be unchanged — the radius never gates `debug_end`.

#### Scenario: Radius returned through the tool
- **WHEN** `map_blast_radius` runs with a valid pattern covering the culprit
- **THEN** the result lists every daemon-recorded hit with file, line, and excerpt, plus the truncation flag

#### Scenario: Gate rejection surfaces verbatim
- **WHEN** the daemon rejects the pattern for missing the culprit file
- **THEN** the tool error carries the daemon's message naming that file

### Requirement: annotate_blast_radius tool
The MCP server SHALL register `annotate_blast_radius(session_id, annotations[])` with per-entry `{file, line, verdict, note?}`; verdict values SHALL be validated client-side against the enum before reaching the daemon, and daemon set-membership rejections SHALL surface as tool errors.

#### Scenario: Invalid verdict rejected client-side
- **WHEN** an annotation carries `verdict: "probably-fine"`
- **THEN** the call is rejected with the allowed values before reaching the daemon

### Requirement: register_probe accepts predictions and verifies the probe location
`register_probe` SHALL accept optional `expected_if_true` and `expected_if_false` parameters (validated as a pair — see session-state). The daemon SHALL resolve `file` against the session's stored `cwd` (absolute paths used as-is) and reject registration when the file does not exist or `line` exceeds the file's line count, with an error naming the resolved path.

#### Scenario: Nonexistent file rejected
- **WHEN** `register_probe` is called with `file: "src/ghost.js"` and no such file exists under the session cwd
- **THEN** the tool returns an actionable error naming the resolved absolute path, and no probe is registered

#### Scenario: Line beyond end of file rejected
- **WHEN** `register_probe` cites line 900 of a 120-line file
- **THEN** the tool returns an error stating the file has 120 lines, and no probe is registered

#### Scenario: Valid predicted probe registers
- **WHEN** `register_probe` is called with an existing file, an in-range line, and a differing prediction pair
- **THEN** the probe is registered and the response echoes the predictions

### Requirement: update_hypothesis requires citations for kills and confirms
`update_hypothesis` SHALL accept `probe_id` and `run_id` parameters, required when `status` is `killed` or `confirmed` and rejected client-side when missing, before reaching the daemon. Daemon-side cross-check failures (unknown probe, unknown or open run, missing predictions, no reproduced run) SHALL surface verbatim as actionable tool errors.

#### Scenario: Missing citation rejected client-side
- **WHEN** `update_hypothesis` is called with `status: "killed"` and no `probe_id`
- **THEN** the call is rejected with a clear message before reaching the daemon

#### Scenario: Store rejection surfaces verbatim
- **WHEN** the daemon rejects a confirm because the session has no reproduced run
- **THEN** the tool error carries the daemon's repair instruction unaltered

### Requirement: await_run accepts a fix prediction
`await_run` SHALL accept an optional `prediction` parameter, forwarded to the daemon before any summary is returned by that call. The result payload SHALL include the session's computed repro rate with raw counts.

#### Scenario: Prediction recorded through the tool
- **WHEN** `await_run` is called with `prediction: "p1 token now populated; p5 fires zero times"` on a predictionless open run
- **THEN** the run carries the prediction before the call's summary is returned

#### Scenario: Repro rate reported
- **WHEN** `await_run` returns for a session with two `reproduced` and one `not-reproduced` closed runs
- **THEN** the result includes the repro rate as 2/3 with the raw counts

### Requirement: debug_start records the pinned commit
The `debug_start` result and stored session SHALL include the resolved commit SHA when the session `cwd` is a git work tree, and omit it otherwise. `debug_resume` SHALL return the commit and the computed repro rate alongside the existing session state.

#### Scenario: Commit in the start payload
- **WHEN** `debug_start` is called from a git work tree
- **THEN** the response includes the HEAD SHA that was pinned on the session

### Requirement: debug_end enforces the solved-close contract and accepts prevention references
`debug_end` SHALL accept optional `regression_test_ref` and `guardrail` parameters alongside the existing resolution fields. Supplying any resolution field requires all of `root_cause`, `fix_summary`, `confirmed_hypothesis_id`; the daemon's board validation failures (unconfirmed hypothesis, partial resolution, unknown guardrail type) SHALL surface as actionable tool errors and the session SHALL remain open.

#### Scenario: Solved close with references
- **WHEN** `debug_end` supplies the three resolution fields naming a board-confirmed hypothesis, plus `regression_test_ref: "TestRefreshRace"`
- **THEN** the session closes solved with the reference persisted

#### Scenario: Rejected solved close leaves the session open
- **WHEN** `debug_end` supplies a resolution naming a hypothesis that is not `confirmed`
- **THEN** the tool returns the daemon's repair instruction and the session remains open with its board intact

### Requirement: diff_runs surfaces the fixed-check prediction
When either compared run carries a recorded prediction, `diff_runs` SHALL include it in the result so the divergence is judged against the recorded claim.

#### Scenario: Prediction shown with the diff
- **WHEN** `diff_runs` compares a reproduce run against a fixed-check run carrying a prediction
- **THEN** the result includes the fixed-check run's prediction text alongside the probe diffs

### Requirement: Version mismatch is disclosed, never enforced
When the daemon health check succeeds but the reported version differs from the MCP process's own compiled version, the client SHALL log one informational line per process stating that the daemon and client versions differ and that a session restart loads the new tool schemas. The client SHALL NOT restart, kill, or refuse to use the daemon based on version comparison.

#### Scenario: Stale client notes the difference once
- **WHEN** an MCP process built as v0.8.0 talks to a daemon reporting v0.9.0 across many tool calls
- **THEN** exactly one informational line is emitted and every tool call proceeds normally

#### Scenario: Matching versions stay silent
- **WHEN** client and daemon report the same version
- **THEN** no note is emitted
