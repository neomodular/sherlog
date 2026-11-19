# log-query (delta)

## MODIFIED Requirements

### Requirement: Run verdicts
The daemon SHALL close the open run with a user-supplied verdict — `reproduced`, `not-reproduced`, or `fixed-check` — and SHALL stamp every stored event with its run. The close SHALL record the verdict's source (`agent` or `witness`), and when the `verdicts` setting is `witnessed` the daemon SHALL refuse a close whose source is `agent`.

#### Scenario: Run closed with verdict
- **WHEN** `close_run(verdict: reproduced)` is called after an await with `verdicts` set to `agent`
- **THEN** the run is recorded as closed with verdict `reproduced`, source `agent`, and appears in run history with its event counts

#### Scenario: Witnessed close
- **WHEN** the user runs `sherlog verdict reproduced` in their own terminal
- **THEN** the run is recorded as closed with verdict `reproduced` and source `witness`

#### Scenario: Agent close refused in witnessed mode
- **WHEN** `verdicts` is `witnessed` and `close_run(verdict: reproduced)` is called
- **THEN** the call is refused and the run stays open
