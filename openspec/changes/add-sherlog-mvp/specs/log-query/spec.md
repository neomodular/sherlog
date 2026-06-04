# log-query

## ADDED Requirements

### Requirement: Blocking await for evidence
The daemon SHALL support a blocking await operation that opens a run on the session and returns when log flow has gone quiet after first activity (debounce ~2 seconds) or when the timeout elapses, whichever comes first. Re-invoking await while a run is open SHALL re-attach to that run instead of opening a new one.

#### Scenario: Evidence arrives during await
- **WHEN** `await_run(timeout_s: 120)` is called and probes fire 30 seconds later, then stop
- **THEN** the call returns shortly after the flow quiets, with a new run opened and a per-probe summary of events captured in that run

#### Scenario: Timeout with no evidence
- **WHEN** `await_run(timeout_s: 60)` is called and no events arrive in 60 seconds
- **THEN** the call returns indicating zero events so the skill can check daemon connectivity or re-prompt the user

#### Scenario: Re-invocation re-attaches
- **WHEN** `await_run` times out, the user needs more time, and `await_run` is called again
- **THEN** the second call attaches to the same open run and previously captured events remain part of it

### Requirement: Run verdicts
The daemon SHALL close the open run with a user-supplied verdict — `reproduced`, `not-reproduced`, or `fixed-check` — and SHALL stamp every stored event with its run.

#### Scenario: Run closed with verdict
- **WHEN** `close_run(verdict: reproduced)` is called after an await
- **THEN** the run is recorded as closed with verdict `reproduced` and appears in run history with its event counts

### Requirement: Filtered log queries
The daemon SHALL answer log queries filtered by probe ID, run, and result limit, returning event counts and selected events (respecting flood-control truncation) rather than requiring full dumps. Query responses SHALL disclose when truncation occurred.

#### Scenario: Did a probe ever fire
- **WHEN** `query_logs(probe: p3)` is called and p3 never fired
- **THEN** the response reports a count of 0 for p3 without returning any other probe's events

#### Scenario: Truncation disclosed
- **WHEN** `query_logs(probe: p2, run: 1)` is called and p2 fired 48,201 times in run 1
- **THEN** the response includes the exact total, the retained first/last events, and an explicit truncation indicator

### Requirement: Per-run probe summaries
The daemon SHALL produce a per-run summary — for each registered probe: event count, first and last event timestamps, and sample bodies — suitable for direct inclusion in an MCP tool result.

#### Scenario: Summary after reproduction
- **WHEN** a run closes in which p1 fired twice, p2 fired 14 times, and p3 never fired
- **THEN** the summary lists p1:2, p2:14, p3:0 with first/last samples for p1 and p2, enabling the skill to reason about which hypotheses survive
