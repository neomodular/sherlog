# probe-leak-guard (delta)

## ADDED Requirements

### Requirement: A refusal is recorded in the audit log
When the guard refuses a commit or push, it SHALL append one `guard_refused` audit record per refusal carrying the layer (`hook` or `git`), the operation (`commit` or `push`), the number of findings, and the matched session IDs, without file paths or line content. A failed append SHALL NOT change the refusal and SHALL NOT be reported on the hot path.

#### Scenario: Hook refusal recorded
- **WHEN** the harness hook refuses a commit with two probes from session `u8xggq67`
- **THEN** one `guard_refused` record notes layer `hook`, operation `commit`, two findings, and `u8xggq67`

#### Scenario: Unwritable log does not change the outcome
- **WHEN** the audit log cannot be written during a refusal
- **THEN** the commit is still refused with the same message
