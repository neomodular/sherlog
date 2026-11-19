# mcp-server (delta)

## ADDED Requirements

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
