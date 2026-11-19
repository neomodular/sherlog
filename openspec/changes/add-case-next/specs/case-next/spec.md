# case-next

## Purpose

Derive, from a session's recorded state alone, where an investigation stands in the detective loop and what can happen next — so every surface (agent, CLI, Case Board) reads one deterministic answer instead of re-interpreting the board.

## ADDED Requirements

### Requirement: The evaluator is a deterministic function of recorded state
The evaluator SHALL compute its result solely from the session's recorded fields (hypotheses, probes, runs, blast radius, resolution, closed time). It SHALL NOT consult a model, the wall clock, the network, or probe event contents, and the same session state SHALL always yield the same result.

#### Scenario: Same board, same answer
- **WHEN** the evaluator runs twice on an unchanged session, minutes apart
- **THEN** both results are identical

### Requirement: The result names a stage, ready actions, human waits, and anomalies
The result SHALL carry exactly one `stage` from the fixed set `board`, `instrument`, `reproduce`, `judge`, `fix`, `cleanup`, `closed`; a `ready` list of agent actions, each with the MCP tool or `apply_fix` as `action`, an optional `target` (hypothesis, probe, or run ID), a one-line `reason`, and an `optional` flag; an `awaiting_human` list; and an `anomalies` list, each with a `code` and `detail`.

#### Scenario: Instrument stage lists each missing probe
- **WHEN** suspects `h1`, `h2`, `h3` are active and only `h1` has a predicted probe
- **THEN** the stage is `instrument` and `ready` holds one `register_probe` action targeting `h2` and one targeting `h3`

### Requirement: Closure, open runs, and the board take precedence
The evaluator SHALL check in this order and stop at the first match: a closed session → `closed`; an open run carrying a prediction → `fix`; any other open run → `reproduce`; fewer than three suspects, or none active and none confirmed → `board`.

#### Scenario: An open run outranks everything but closure
- **WHEN** run `r3` is open and a suspect is confirmed
- **THEN** the stage is `reproduce` or `fix` according to whether `r3` carries a prediction, never `cleanup`

### Requirement: Remaining stages follow the loop order
When no earlier rule matches, the evaluator SHALL stop at the first match of: a confirmed suspect whose latest prediction run closed `fixed-check` → `cleanup`; any other confirmed suspect → `fix`; an active suspect without an unremoved predicted probe → `instrument`; a closed run after which no hypothesis was created or updated → `judge`; otherwise → `reproduce`.

#### Scenario: Every suspect eliminated returns to the board
- **WHEN** all three suspects are killed and none is confirmed
- **THEN** the stage is `board` and `ready` holds `set_hypotheses` with a reason stating every suspect was eliminated

#### Scenario: Unjudged run awaits judgment
- **WHEN** run `r2` closed `reproduced` and no hypothesis was updated after it closed
- **THEN** the stage is `judge`, `ready` holds `update_hypothesis` citing `r2`, and `await_run` is also listed, marked `optional`, for when the evidence is inconclusive

#### Scenario: Judged run moves on
- **WHEN** a suspect was killed citing `r2` after `r2` closed, and every active suspect still has a predicted probe
- **THEN** the stage is `reproduce` with `await_run` ready

### Requirement: Human steps are never inferred
The evaluator SHALL treat reproduction, the run verdict, and the fix retest as human steps: an open run SHALL put the reproduction and verdict in `awaiting_human`, and SHALL NOT be reported as reproduced, judged, or verified by any downstream state. Only a recorded verdict advances past a run; only a run carrying a recorded prediction counts as a fix under verification.

#### Scenario: Open run waits for the user
- **WHEN** run `r3` is open with no prediction
- **THEN** the stage is `reproduce`, `awaiting_human` asks the user to reproduce and report the verdict, and `ready` holds `close_run` for `r3` with a reason that it follows the user's verdict

#### Scenario: Fix retest waits for the user
- **WHEN** run `r4` is open and carries a fix prediction
- **THEN** the stage is `fix` and `awaiting_human` asks the user to retest the fix

#### Scenario: Unapplied fix is not inferred from a confirm
- **WHEN** a suspect is confirmed and no run carries a prediction
- **THEN** the stage is `fix` and `ready` holds `apply_fix` followed by `await_run` with a prediction, never `cleanup`

### Requirement: A closed case records the stage it closed from
For a closed session, the result SHALL also carry `closed_from`: the stage the precedence rules yield when the closed time is disregarded, so an unsolved close shows where the investigation stopped.

#### Scenario: Unsolved close mid-instrumentation
- **WHEN** a case is closed unsolved while one active suspect still lacked a predicted probe
- **THEN** the stage is `closed` and `closed_from` is `instrument`

#### Scenario: Solved close
- **WHEN** a case closed after its fix check passed and every probe was removed
- **THEN** `closed_from` is `cleanup`

### Requirement: The fix stage carries the blast radius as optional advice
In the `fix` stage, when no blast radius is recorded and no run carries a prediction yet, `ready` SHALL list `map_blast_radius` marked `optional`; when a recorded radius has unreviewed hits, `ready` SHALL list `annotate_blast_radius` marked `optional` with the unreviewed count. The radius SHALL never block any other ready action.

#### Scenario: Radius offered before the fix only
- **WHEN** a suspect is confirmed, no radius exists, and a prediction run has already been recorded
- **THEN** `map_blast_radius` is not listed

### Requirement: A failed fix check returns to the fix stage
When the latest prediction run closed with a verdict other than `fixed-check`, the evaluator SHALL report the `fix` stage with a reason naming that run and its verdict.

#### Scenario: Bug still reproduces after the fix
- **WHEN** prediction run `r5` closed `reproduced`
- **THEN** the stage is `fix` and the `apply_fix` reason states that `r5` still reproduced

### Requirement: Cleanup lists each live probe, then the close
In the `cleanup` stage, `ready` SHALL list one `remove_probe` per unremoved probe; when none remain, `ready` SHALL list `debug_end` with the confirmed hypothesis as target.

#### Scenario: Probes removed, close is next
- **WHEN** the fix check closed `fixed-check` and every probe is marked removed
- **THEN** the stage is `cleanup` and `ready` holds only `debug_end` targeting the confirmed suspect

### Requirement: Anomalies are reported, never acted on
The evaluator SHALL report recorded state the gated loop cannot legitimately produce as anomalies, without changing the stage, using the codes `closed-with-live-probes`, `uncited-verdict`, `confirm-without-reproduction`, `multiple-confirmed`, `resolution-unconfirmed`, `fixed-check-without-prediction`, and `multiple-open-runs`.

#### Scenario: Each code has its trigger
- **WHEN** the session holds, respectively: a close with unremoved probes; a killed or confirmed suspect without a citation; a confirm with no reproduced run; two confirmed suspects; a solved resolution naming an unconfirmed suspect; a `fixed-check` run without a prediction; two open runs
- **THEN** the matching code is reported once, naming the IDs involved

#### Scenario: Legacy uncited confirm flagged
- **WHEN** a session written before evidence citations has a confirmed suspect with no citation
- **THEN** `anomalies` holds `uncited-verdict` naming the suspect and the stage is computed as usual

#### Scenario: Leftover probes after close
- **WHEN** a closed session still has two unremoved probes
- **THEN** the stage is `closed` and `anomalies` holds `closed-with-live-probes` naming both

### Requirement: Advice never loosens enforcement
The store's gates SHALL NOT read the evaluator's result, and the evaluator SHALL NOT modify the session. An action listed as ready MAY still be refused by a gate.

#### Scenario: Ready is not permission
- **WHEN** `ready` lists `update_hypothesis` and the agent confirms without a citation
- **THEN** the gate still refuses the call

### Requirement: The CLI prints the evaluation without a daemon
`sherlog next [--session ID] [--json]` SHALL read the persisted store directly, default to the most recently created open session (or report that none is open), print the stage track, ready actions, human waits, and anomalies, and with `--json` print the evaluator result as-is.

#### Scenario: No daemon running
- **WHEN** no daemon runs and an open session exists
- **THEN** `sherlog next` prints its stage and ready actions

#### Scenario: Nothing open
- **WHEN** no session is open and no `--session` is given
- **THEN** it prints that no case is open and exits 0
