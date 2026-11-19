# case-stats

## Purpose

Compute sherlog's end-to-end number — how often an investigation ends in a verified fix — and the loop-health signals behind it, from the audit log alone, with every definition stated next to the number.

## ADDED Requirements

### Requirement: Stats are computed from the audit log alone
Stats SHALL be derived only from audit records and the evaluation time, never from session state files, so cases removed by retention still count. Cases whose records are backfilled SHALL be included, with their count disclosed.

#### Scenario: Pruned cases still count
- **WHEN** ten cases were solved and seven of them have since been pruned
- **THEN** stats report ten solved cases

### Requirement: Each case has exactly one outcome
A case SHALL be classified as `solved` when it closed solved and at least one of its runs closed `fixed-check`; `solved-unverified` when it closed solved with no `fixed-check` run; `unsolved` when it closed without a resolution; `abandoned` when open with no record newer than `abandon_after_days` before the evaluation time; otherwise `in-flight`.

#### Scenario: Solved without a fix check
- **WHEN** a case closed solved and none of its runs closed `fixed-check`
- **THEN** it is `solved-unverified`, not `solved`

#### Scenario: Stale open case
- **WHEN** a case is open and its last record is 9 days old with `abandon_after_days` set to 7
- **THEN** it is `abandoned`

### Requirement: The verified solve rate excludes in-flight cases
Stats SHALL report the verified solve rate as solved ÷ (solved + solved-unverified + unsolved + abandoned), with every outcome count; in-flight cases SHALL be counted but excluded from the rate. With no resolved case the rate SHALL be reported as unavailable, not zero.

#### Scenario: Rate over resolved cases
- **WHEN** there are 6 solved, 2 solved-unverified, 1 unsolved, 1 abandoned, and 3 in-flight cases
- **THEN** the rate is 60% and in-flight is reported as 3

#### Scenario: No history
- **WHEN** no case has resolved
- **THEN** the rate is reported as unavailable

### Requirement: The trend depends only on recorded history
Stats SHALL report the rate per ISO week (UTC) of each resolved case's terminal record — its close, or for an abandoned case its last record — for the most recent 8 weeks that contain at least one resolved case.

#### Scenario: Quiet weeks skipped
- **WHEN** cases resolved in weeks 30, 31, and 36 only
- **THEN** the trend lists exactly those three weeks

### Requirement: Loop health is reported beside the rate
Stats SHALL report: first-try fix rate (cases whose first closed prediction run closed `fixed-check`, over cases with any closed prediction run); median runs and median open-to-close time for solved and solved-unverified cases; median kills before a confirm; refusals per case and the five most frequent gate codes; leak-guard refusals; closed cases that left probes registered but not removed; and the stage at which unsolved and abandoned cases stopped.

#### Scenario: Where cases die
- **WHEN** two unsolved cases closed from `instrument` and one abandoned case last acted in `judge`
- **THEN** the stopping-stage breakdown shows `instrument: 2` and `judge: 1`

#### Scenario: Most-refused gates
- **WHEN** `evidence-citation-required` was refused 5 times and `insufficient-hypotheses` twice
- **THEN** the top refusals list them in that order with their counts

### Requirement: Definitions travel with the numbers
The text output SHALL end with the outcome and rate definitions in plain language, and the JSON output SHALL carry the same definitions in a `definitions` field.

#### Scenario: JSON is self-describing
- **WHEN** `sherlog stats --json` runs
- **THEN** the output contains `definitions` stating how each outcome and the rate are computed

### Requirement: The stats CLI works without a daemon
`sherlog stats [--json] [--since <duration>]` SHALL read the audit log directly; `--since` SHALL restrict stats to cases opened within the duration; skipped audit lines SHALL be reported.

#### Scenario: Window applied
- **WHEN** `sherlog stats --since 30d` runs
- **THEN** only cases opened in the last 30 days are classified

### Requirement: The stats API carries outcomes
The daemon's stats document SHALL include an `outcomes` object holding the same computation the CLI reports for all history.

#### Scenario: Health view data
- **WHEN** the Case Board requests the stats document
- **THEN** it includes `outcomes` with the rate, counts, trend, loop-health metrics, and definitions
