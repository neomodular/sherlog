# case-next (delta)

## ADDED Requirements

### Requirement: Witnessed mode changes how an open run is awaited
When `verdicts` is `witnessed`, the evaluation of a case with an open run SHALL put a `report_verdict` (or `retest_fix`) wait in `awaiting_human` whose reason names `sherlog verdict` run from the user's own terminal, and SHALL NOT list `close_run` in `ready`.

#### Scenario: Open run in witnessed mode
- **WHEN** run `r2` is open and `verdicts` is `witnessed`
- **THEN** `awaiting_human` names the `sherlog verdict` command and `ready` does not contain `close_run`

#### Scenario: Default mode unchanged
- **WHEN** run `r2` is open and `verdicts` is `agent`
- **THEN** `ready` lists `close_run` for `r2` as before
