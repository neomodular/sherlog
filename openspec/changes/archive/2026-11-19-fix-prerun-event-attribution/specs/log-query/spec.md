# log-query (delta)

## ADDED Requirements

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
