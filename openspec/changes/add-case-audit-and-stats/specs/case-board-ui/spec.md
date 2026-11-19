# case-board-ui (delta)

## ADDED Requirements

### Requirement: The Health view shows outcomes
The Health view SHALL show an Outcomes panel with the verified solve rate and outcome counts, the weekly trend, first-try fix rate, the top refused gates, leaks prevented, and where unsolved and abandoned cases stopped, rendered from the stats document's `outcomes` with no client-side computation. An unavailable rate SHALL read as "not enough history", never 0%.

#### Scenario: Fresh install
- **WHEN** no case has resolved yet
- **THEN** the panel reads "not enough history" and shows the in-flight count

### Requirement: The case detail shows its audit timeline
The case detail SHALL show the case's audit records in order, each with its time, stage, and event, with refusals visually distinct and showing their gate code, and backfilled records labeled as reconstructed.

#### Scenario: Refusal pinned in the timeline
- **WHEN** a case had a refused confirm in `judge` followed by an accepted one
- **THEN** the timeline shows the refusal with its gate code at its position, before the accepted confirm
