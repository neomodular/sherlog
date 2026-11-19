# debug-skill (delta)

## ADDED Requirements

### Requirement: Discriminating probes are registered with their prediction pair
The skill SHALL supply `expected_if_true` / `expected_if_false` when registering each hypothesis's discriminating probe, stating concretely how the payload differs under each outcome. Plain path-tracer probes MAY omit predictions, but every hypothesis SHALL have at least one predicted probe before the reproduction wait begins.

#### Scenario: Predicted probe authored per suspect
- **WHEN** the skill plants the discriminating probe for "race between token refresh and request"
- **THEN** it registers the probe with distinct `expected_if_true` and `expected_if_false` payload descriptions

### Requirement: Kills and confirms cite their evidence structurally
The skill SHALL pass `probe_id` and `run_id` on every `killed`/`confirmed` transition, citing the closed run whose summary it is reasoning from, and keep the free-text note as the human-readable explanation of the same evidence.

#### Scenario: Kill carries the citation
- **WHEN** the skill kills a suspect because `p4` showed a fresh cache in run `r2`
- **THEN** it calls `update_hypothesis` with `probe_id: "p4"`, `run_id: "r2"`, and a note explaining the payload

### Requirement: The fix prediction is recorded before the fixed-check reproduction
Before asking the user to retest a fix, the skill SHALL pass its evidence-change prediction as `await_run(prediction=...)` — the board, not the conversation, holds the claim the fixed-check is judged against. When `close_run(fixed-check)` is rejected for a missing prediction, the skill SHALL re-await with the prediction and ask for one more reproduction rather than restating the prediction in prose.

#### Scenario: Prediction travels through the tool
- **WHEN** the skill expects "p1's token populated; p5 fires zero times" after a fix
- **THEN** it supplies exactly that as the `prediction` parameter of the fixed-check `await_run`

### Requirement: Gate rejections are repaired, never routed around
When the daemon rejects a transition (board too small, missing citation, no reproduced run, prediction-less confirm, invalid solved close), the skill SHALL treat the rejection as a discipline breach: perform the named repair (extend the board, register the predicted probe, run again, fix the resolution) and retry the transition. It SHALL NOT weaken the claim to fit a lenient path, close unsolved to bypass a failed solved close the user believes is solved, or retry the identical call.

#### Scenario: Rejected confirm repaired
- **WHEN** a confirm is rejected because the citing probe carries no predictions
- **THEN** the skill registers a predicted probe for that hypothesis, reruns the reproduction, and confirms citing the new evidence

### Requirement: Repro rate is reported, not asserted
The skill SHALL state determinism from the computed repro rate returned by the tools (e.g. "reproduced 2/3 runs — intermittent") and SHALL NOT assert always/intermittent from memory. For an intermittent bug, absence of a probe's events in a single `not-reproduced` run SHALL NOT kill a suspect on its own.

#### Scenario: Intermittency stated from the computed rate
- **WHEN** the board shows repro rate 2/5 after several runs
- **THEN** the skill reports the bug as intermittent citing 2/5, and keeps gathering runs rather than killing suspects from one quiet run

### Requirement: Prevention references are recorded only when real
When a regression test or guardrail actually exists at close time, the skill SHALL pass `regression_test_ref` / `guardrail` to `debug_end`. It SHALL NOT invent references — a solved close with no prevention artifact simply omits them.

#### Scenario: Real test referenced
- **WHEN** the fix landed with a regression test `TestRefreshRace`
- **THEN** the skill closes with `regression_test_ref: "TestRefreshRace"`

#### Scenario: No artifact, no reference
- **WHEN** no regression test was written
- **THEN** the skill closes solved without the field rather than fabricating one
