# log-query Specification

## Purpose
Turn ingested probe events into evidence: block while the user reproduces, record run verdicts, query and summarize events per run, compare runs, and attribute pre-run events with disclosure.

## Requirements

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

### Requirement: Run diff
The daemon SHALL compute a per-probe comparison between two runs of the same session: fired-in-each flags, counts, first/last sample bodies from each side, and a divergence flag (fired in exactly one run, or count ratio ≥ 10×). Diff output SHALL carry the same truncation disclosures as queries. Requests naming runs from different sessions or unknown runs SHALL be rejected with a clear error.

#### Scenario: Differential diagnosis
- **WHEN** a diff is requested between run 1 (verdict reproduced) and run 3 (verdict fixed-check) where probe p3 fired only in run 3
- **THEN** p3 is flagged divergent with its per-run counts and samples, and unflagged probes still report both sides

#### Scenario: Invalid run pair
- **WHEN** a diff is requested for runs belonging to different sessions
- **THEN** the daemon rejects the request with an error identifying the mismatch

### Requirement: Pre-run events are adopted when a new run opens
When a new run opens (not a re-attach), the daemon SHALL adopt orphan events — events stored with no run — whose timestamps are after the last run boundary (the previous run's close time, else the session start) and within a 15-minute cap, attributing them to the newly opened run. Adoption SHALL survive daemon restart without rewriting existing log lines.

#### Scenario: Fast reproduction before await_run
- **WHEN** probes fire after run r1 closed and before `await_run` opens r2, and r2 then opens
- **THEN** those events are attributed to r2 and appear in r2's summary and await result

#### Scenario: Boundary protects prior evidence
- **WHEN** events were attributed to r1 (closed) and new orphans arrived after r1's close
- **THEN** opening r2 adopts only the post-boundary orphans; r1's events are untouched

#### Scenario: Cap excludes ancient stragglers
- **WHEN** an orphan event is 40 minutes old at the time a new run opens
- **THEN** it is not adopted and remains unattributed

#### Scenario: Re-attach does not adopt
- **WHEN** `await_run` re-attaches to an already-open run
- **THEN** no adoption occurs (adoption happens only at open)

#### Scenario: Adoption survives restart
- **WHEN** the daemon restarts after an adoption
- **THEN** replay restores the same attribution

### Requirement: Adopted counts are disclosed
Run summaries and await results SHALL report, per probe, how many of its events were adopted (`adopted`), alongside existing counts and truncation disclosures. Where truncation makes an adopted total inexact, it SHALL be reported as a disclosed minimum.

#### Scenario: Fully adopted run
- **WHEN** r2's only events were adopted (the reproduction finished before the run opened)
- **THEN** each probe summary shows count and adopted equal, so the caller knows attribution was inferred
