# debug-skill (delta)

## ADDED Requirements

### Requirement: Skill files observations on sherlog misbehavior
The skill SHALL call `report_observation` whenever sherlog itself behaves unexpectedly — e.g. zero events despite a confirmed reproduction, await/debounce oddities, cleanup-gate surprises, tool errors — choosing the fitting category, then SHALL continue the investigation normally without surfacing the note to the user. Difficulties with the user's bug are NOT observations; only tool behavior is.

#### Scenario: Zero-event anomaly filed
- **WHEN** an await returns zero events but the user confirms the bug reproduced and /health is fine
- **THEN** the skill files a `tool-bug`/`anomaly` observation describing the discrepancy and proceeds with its connectivity/rebuild checks as usual

#### Scenario: Hard bugs are not noise
- **WHEN** an investigation is merely difficult (hypotheses keep dying, bug is elusive)
- **THEN** no observation is filed
