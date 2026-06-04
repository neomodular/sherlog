# mcp-server (delta)

## ADDED Requirements

### Requirement: diff_runs tool
The MCP server SHALL expose `diff_runs(run_a, run_b)` returning the daemon's run-diff for the active session as a compact structured payload (divergent probes first), consistent with query-not-dump.

#### Scenario: Claude asks what changed
- **WHEN** `diff_runs(1, 3)` is called after a fix attempt
- **THEN** the result lists divergent probes with per-run counts and samples, enabling root-cause confirmation without raw log reads

### Requirement: Resolution fields on debug_end
`debug_end` SHALL accept optional `root_cause`, `fix_summary`, and `confirmed_hypothesis_id`, recording them as the session's resolution. Existing callers without the new fields SHALL keep working.

#### Scenario: Closing a solved case
- **WHEN** `debug_end` is called with root cause and fix summary after a confirmed fix
- **THEN** the session closes with the resolution recorded and the response still includes the cleanup checklist

### Requirement: Recall in debug_start response
`debug_start` SHALL include the case-recall matches (possibly related closed cases with root causes and fix summaries) in its structured response, as a clearly-labeled advisory section.

#### Scenario: Familiar symptom
- **WHEN** `debug_start` is called and recall finds a similar solved case
- **THEN** the response's related-cases section carries that case's root cause and fix summary alongside the session id and probe template
