# case-board-ui (delta)

## ADDED Requirements

### Requirement: Probe predictions are visible on the board
A probe carrying a prediction pair SHALL display both `expected_if_true` and `expected_if_false` in the case detail's probe listing, visually paired so the discriminating contrast reads at a glance. Prediction-less probes render unchanged.

#### Scenario: Prediction pair rendered
- **WHEN** the case detail shows a probe with predictions
- **THEN** both expected outcomes are visible and labeled (if-true / if-false)

### Requirement: Hypothesis verdicts show their evidence citation
A killed or confirmed hypothesis SHALL display its citation (probe and run, using display names) alongside the evidence note, linking the verdict to the run it came from.

#### Scenario: Confirmed suspect shows its proof
- **WHEN** a hypothesis is confirmed citing `p1` in `r2`
- **THEN** the board renders the verdict with "Probe 1 · Run 2" (display names) next to the note

### Requirement: The case header shows repro rate and pinned commit
The case header SHALL show the computed repro rate with raw counts (e.g. `reproduced 2/3`) once at least one repro-attempt run has closed, and the pinned commit SHA (shortened) when the session carries one.

#### Scenario: Header carries the determinism signal
- **WHEN** a case has closed runs with verdicts `reproduced`, `reproduced`, `not-reproduced`
- **THEN** the header shows `2/3` and the short commit when pinned

### Requirement: The resolution panel shows prevention references
The closed-case resolution view SHALL display `regression_test_ref` and the guardrail (type badge + ref text) when present, as plain text — never as executable links that mutate anything (the board remains GET-only with no external origins).

#### Scenario: Guardrail displayed inert
- **WHEN** a solved case carries `guardrail: {type: "lint", ref: "no-floating-refresh"}`
- **THEN** the resolution panel shows a "lint" badge with the ref text, with no outbound fetch or mutation

### Requirement: Fixed-check runs display their prediction
A run carrying a recorded prediction SHALL display it in the run detail and in the compare-runs view, so the observed divergence is read against the recorded claim.

#### Scenario: Prediction beside the diff
- **WHEN** the compare view diffs a reproduce run against a predicted fixed-check run
- **THEN** the prediction text is rendered above the probe divergence list
