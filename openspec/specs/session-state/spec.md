# session-state Specification

## Purpose
Hold every investigation's state — board, probes, runs, resolution, blast radius — in the daemon with durable persistence, so a case survives restarts, context loss, and resumes days later.

## Requirements

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

### Requirement: Resolution recorded at session close
Sessions SHALL support an optional resolution — root cause, fix summary, confirmed hypothesis id, closed-at time — persisted in `state.json` when the session closes. Closing without a resolution SHALL remain valid and be represented as unsolved. Older state files without the field SHALL load unchanged.

#### Scenario: Solved case records its resolution
- **WHEN** a session closes with root cause "float rounding in discount calc" and a fix summary
- **THEN** the resolution persists, survives daemon restart, and is returned by session reads

#### Scenario: Unsolved close
- **WHEN** a session closes without resolution fields
- **THEN** the session is stored as closed-unsolved and excluded from recall's root-cause matching

### Requirement: Sessions carry a title
Sessions SHALL store an optional short title alongside the description, persisted in `state.json`. When a session is created without a title, the daemon SHALL derive one at read time from the description (first ~60 characters, word-boundary truncated with an ellipsis) so every session payload carries a non-empty title. Legacy state files without the field SHALL load unchanged.

#### Scenario: Titled session round-trips
- **WHEN** a session is created with title "Login 401 after idle timeout" and a detailed description
- **THEN** both persist, survive daemon restart, and are returned distinctly in session reads

#### Scenario: Legacy session gets a derived title
- **WHEN** a pre-title `state.json` session with a 300-character description is loaded and read
- **THEN** its payload carries a word-boundary-truncated ~60-char title ending in an ellipsis, and the stored file is not rewritten

### Requirement: Sessions persist the blast radius
A session MAY carry one `BlastRadius {pattern, note, searched_at, truncated, hits[]}` where each hit is `{file, line, excerpt, verdict?, note?}`. The field SHALL be additive in `state.json`, survive daemon restart, and be returned by session reads (including `debug_resume`). Legacy state files without the field SHALL load unchanged; replace semantics on re-run per the blast-radius capability.

#### Scenario: Radius round-trips
- **WHEN** a radius with 4 hits (2 annotated) is stored and the daemon restarts
- **THEN** session reads return the identical radius including verdicts and the truncation flag

#### Scenario: Legacy session loads without a radius
- **WHEN** a pre-change `state.json` session is loaded
- **THEN** it resumes normally with no blast radius and is not rewritten

### Requirement: Probes carry discriminating predictions
A probe MAY carry an `expected_if_true` / `expected_if_false` prediction pair describing how its payload differs depending on whether its hypothesis is true. The store SHALL reject registration when only one of the pair is supplied, or when the two values are equal after trimming whitespace and ignoring case. The pair SHALL persist in `state.json` and survive daemon restart. Probes without predictions remain valid.

#### Scenario: Valid prediction pair round-trips
- **WHEN** a probe is registered with `expected_if_true: "token=null past TTL"` and `expected_if_false: "token populated, fresh"`
- **THEN** registration succeeds and both values persist across a daemon restart

#### Scenario: Non-discriminating pair rejected
- **WHEN** a probe is registered with `expected_if_true` equal to `expected_if_false` (modulo case and whitespace)
- **THEN** registration fails with an error stating the prediction proves nothing because both outcomes look the same

#### Scenario: One-sided prediction rejected
- **WHEN** a probe is registered with `expected_if_true` but no `expected_if_false`
- **THEN** registration fails with an error naming the missing half

### Requirement: Hypothesis kills and confirms require evidence citations
Transitioning a hypothesis to `killed` or `confirmed` SHALL require a `probe_id` and `run_id` citation. The store SHALL reject the transition unless the probe is registered in the session and the run exists and is closed with a verdict. A citation to a probe with zero events in the cited run is valid — "fired zero times" is evidence. The accepted citation SHALL persist on the hypothesis alongside the free-text note. Transitions to `active` require no citation.

#### Scenario: Kill citing a zero-fired probe accepted
- **WHEN** `h2` is killed citing `p4` in closed run `r2` where `p4` recorded `total: 0`
- **THEN** the transition succeeds and the hypothesis carries the citation `{probe: p4, run: r2}`

#### Scenario: Citation to an unknown run rejected
- **WHEN** `h2` is killed citing run `r9` which does not exist in the session
- **THEN** the transition fails with an error naming the unknown run, and the hypothesis status is unchanged

#### Scenario: Citation to an open run rejected
- **WHEN** `h1` is confirmed citing a run that has not been closed with a verdict
- **THEN** the transition fails with an error instructing that the run's verdict must be recorded first

#### Scenario: Kill without a citation rejected
- **WHEN** `update_hypothesis` sets status `killed` with no `probe_id`/`run_id`
- **THEN** the transition fails with an error stating evidence citations are required for kills and confirms

### Requirement: Confirming a root cause requires a reproduced run and a predicted probe
The store SHALL reject a transition to `confirmed` unless the session has at least one closed run with verdict `reproduced`, and the cited probe carries a prediction pair.

#### Scenario: Confirm without any reproduced run rejected
- **WHEN** `h1` is confirmed in a session whose closed runs are all `not-reproduced`
- **THEN** the transition fails with an error stating a root cause cannot be confirmed for a bug never reproduced under instrumentation

#### Scenario: Confirm citing a prediction-less probe rejected
- **WHEN** `h1` is confirmed citing a probe registered without a prediction pair
- **THEN** the transition fails with an error instructing that the confirming probe must carry `expected_if_true` / `expected_if_false`

#### Scenario: Fully qualified confirm succeeds
- **WHEN** `h1` is confirmed citing predicted probe `p1` in closed run `r2`, and the session has a run closed `reproduced`
- **THEN** the hypothesis becomes `confirmed` with the citation persisted

### Requirement: The board holds at least three suspects
The store SHALL reject a `SetHypotheses` call carrying fewer than three statements. Replace semantics are unchanged: a resubmitted board must itself carry at least three statements.

#### Scenario: Two-suspect board rejected
- **WHEN** `set_hypotheses` is called with two statements
- **THEN** the call fails with an error stating the board needs at least three distinct suspects, and the existing board is unchanged

### Requirement: Runs carry a fix prediction recorded before evidence is returned
A run MAY carry a `prediction` string with a `prediction_at` timestamp. The daemon SHALL stamp the prediction at call receipt — before returning any summary from that call — and SHALL ignore attempts to overwrite an existing prediction. Closing a run with verdict `fixed-check` SHALL be rejected when the run carries no prediction.

#### Scenario: Fixed-check without a prediction rejected
- **WHEN** `close_run` is called with verdict `fixed-check` on a run that has no recorded prediction
- **THEN** the close fails with an error instructing the agent to re-await with a `prediction` and ask the user to reproduce once more

#### Scenario: Prediction is immutable once set
- **WHEN** an `await_run` supplies a prediction for a run that already carries one
- **THEN** the stored prediction and its timestamp are unchanged

#### Scenario: Predicted fixed-check closes
- **WHEN** a run opened with `prediction: "p1 token now populated; p5 fires zero times"` is closed with verdict `fixed-check`
- **THEN** the close succeeds and the run persists the prediction and verdict

### Requirement: Sessions pin the repository commit when available
At session creation the daemon SHALL attempt to resolve the current commit SHA of the session `cwd` (fixed-argv `git rev-parse HEAD`, short timeout) and store it as `Session.Commit`. On any failure the field SHALL be omitted silently — a missing commit never blocks a session.

#### Scenario: Commit recorded in a git repository
- **WHEN** a session is created with `cwd` inside a git work tree
- **THEN** the session persists the current HEAD SHA

#### Scenario: Non-repository cwd tolerated
- **WHEN** a session is created with `cwd` outside any git repository
- **THEN** the session is created normally with no commit field

### Requirement: Repro rate is computed from run verdicts
The repro rate SHALL be computed as `reproduced / (reproduced + not-reproduced)` over the session's closed runs, excluding `fixed-check` runs, and SHALL never be stored — derived at read time and reported with its raw counts.

#### Scenario: Rate reflects verdicts
- **WHEN** a session has closed runs with verdicts `reproduced`, `not-reproduced`, `reproduced`, and a `fixed-check`
- **THEN** the reported repro rate is 2/3, with the fixed-check run excluded

### Requirement: A solved close is validated against the board
A close supplying any resolution field SHALL supply all of `root_cause`, `fix_summary`, and `confirmed_hypothesis_id`, and the store SHALL reject the close unless `confirmed_hypothesis_id` references a hypothesis whose status is `confirmed`. Rejection is an error, never a silent downgrade to an unsolved close. A close with no resolution fields remains always valid and records an unsolved case.

#### Scenario: Resolution naming an unconfirmed hypothesis rejected
- **WHEN** `debug_end` supplies a resolution with `confirmed_hypothesis_id: "h3"` while `h3` is `active` on the board
- **THEN** the close fails with an error instructing that the board must confirm `h3` (with evidence) before the case can close solved

#### Scenario: Partial resolution rejected
- **WHEN** `debug_end` supplies `root_cause` but no `confirmed_hypothesis_id`
- **THEN** the close fails with an error naming the missing fields

#### Scenario: Unsolved close unaffected
- **WHEN** `debug_end` is called with no resolution fields
- **THEN** the session closes unsolved exactly as before

### Requirement: Resolutions carry optional prevention references
`Resolution` MAY carry `regression_test_ref` (free text) and `guardrail` (`{type, ref}` where `type` is one of `test`, `lint`, `alert`, `doc`). The store SHALL reject an unknown guardrail type and SHALL never resolve, fetch, or execute either reference.

#### Scenario: Guardrail recorded
- **WHEN** a solved close supplies `guardrail: {type: "lint", ref: "eslint rule no-floating-refresh"}`
- **THEN** the resolution persists the guardrail and the daemon takes no action on it

#### Scenario: Unknown guardrail type rejected
- **WHEN** a solved close supplies `guardrail: {type: "vibes", ref: "..."}`
- **THEN** the close fails naming the allowed types

### Requirement: Legacy state loads unchanged
Pre-change `state.json` files SHALL load and resume with every new field absent. Gates apply only to new transitions performed after the upgrade.

#### Scenario: Old session resumes under new gates
- **WHEN** a pre-change session with prediction-less probes is resumed and the agent confirms a hypothesis
- **THEN** the confirm is rejected with the standard repair instruction (register a predicted probe, cite a closed run) rather than corrupting or migrating the stored session
