# case-audit (delta)

## ADDED Requirements

### Requirement: Run-close records carry the verdict source
Each `run_closed` audit record SHALL carry the verdict source (`agent`, `witness`, or `unknown` for backfilled legacy runs), and a witnessed-mode refusal of an agent close SHALL be recorded as a `refused` record with the gate code `verdict-not-witnessed`.

#### Scenario: Witnessed close recorded
- **WHEN** the user closes run `r3` with `sherlog verdict fixed-check`
- **THEN** the `run_closed` record for `r3` carries source `witness`

#### Scenario: Refused agent close recorded
- **WHEN** witnessed mode refuses the agent's `close_run`
- **THEN** a `refused` record names `close_run` and the code `verdict-not-witnessed`
