# session-state (delta)

## ADDED Requirements

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
