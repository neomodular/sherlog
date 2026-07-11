# session-state (delta)

## ADDED Requirements

### Requirement: Sessions persist the blast radius
A session MAY carry one `BlastRadius {pattern, note, searched_at, truncated, hits[]}` where each hit is `{file, line, excerpt, verdict?, note?}`. The field SHALL be additive in `state.json`, survive daemon restart, and be returned by session reads (including `debug_resume`). Legacy state files without the field SHALL load unchanged; replace semantics on re-run per the blast-radius capability.

#### Scenario: Radius round-trips
- **WHEN** a radius with 4 hits (2 annotated) is stored and the daemon restarts
- **THEN** session reads return the identical radius including verdicts and the truncation flag

#### Scenario: Legacy session loads without a radius
- **WHEN** a pre-change `state.json` session is loaded
- **THEN** it resumes normally with no blast radius and is not rewritten
