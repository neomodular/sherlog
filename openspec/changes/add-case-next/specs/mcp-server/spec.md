# mcp-server (delta)

## ADDED Requirements

### Requirement: Tool results carry the next step
`debug_resume` and every tool that changes session state (`set_hypotheses`, `register_probe`, `remove_probe`, `await_run`, `close_run`, `update_hypothesis`, `map_blast_radius`, `annotate_blast_radius`) SHALL include an additive `next` object holding the case-next evaluation of the session as it stands after the call. A rejected call SHALL NOT carry `next`. Existing result fields SHALL be unchanged.

#### Scenario: Resume tells the agent where to pick up
- **WHEN** the agent calls `debug_resume` on a case whose last run closed and was not yet judged
- **THEN** the result's `next.stage` is `judge` and `next.ready` cites that run

#### Scenario: Next reflects the call just made
- **WHEN** `register_probe` adds the last missing predicted probe
- **THEN** the result's `next.stage` is `reproduce` with `await_run` ready

#### Scenario: Read-only tools unchanged
- **WHEN** the agent calls `query_logs` or `diff_runs`
- **THEN** the result carries no `next` object

### Requirement: Await results are evaluated after the wait
`await_run` SHALL compute `next` when it returns, so a result for a still-open run reports the human wait (reproduce and report the verdict, or retest the fix).

#### Scenario: Await returns to a human wait
- **WHEN** `await_run` returns its evidence summary for open run `r2`
- **THEN** `next.awaiting_human` asks the user for the verdict and `next.ready` lists `close_run` for `r2`
