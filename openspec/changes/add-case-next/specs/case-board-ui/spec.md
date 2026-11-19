# case-board-ui (delta)

## ADDED Requirements

### Requirement: The case detail shows the loop's stage track
The case detail view SHALL show the stages `board`, `instrument`, `reproduce`, `judge`, `fix`, `cleanup` in order, marking stages before the current one done, the current one active, and later ones pending, using the same evaluation the agent receives. A closed case SHALL show every stage done, or, if closed unsolved, mark the stage it closed from.

#### Scenario: Mid-investigation track
- **WHEN** a case's evaluation stage is `judge`
- **THEN** the track shows board, instrument, reproduce done; judge active; fix and cleanup pending

#### Scenario: Human wait is labeled
- **WHEN** the evaluation has items awaiting a human
- **THEN** the active stage is labeled as waiting on the user with the awaited item's text

### Requirement: Anomalies are visible on the case detail
Every anomaly in the case's evaluation SHALL be listed on the case detail with its code and detail; a case without anomalies SHALL show no anomaly section.

#### Scenario: Closed case with leftover probes
- **WHEN** a closed case's evaluation holds `closed-with-live-probes`
- **THEN** the case detail lists it with the probe names it carries
