# mcp-server (delta)

## ADDED Requirements

### Requirement: map_blast_radius tool
The MCP server SHALL register `map_blast_radius(session_id, pattern, note?)`, passing the pattern to the daemon for execution and returning the recorded radius (hits, truncation flag, unreviewed count). Gate rejections (no confirmed hypothesis, culprit not in hit set, invalid regex) SHALL surface verbatim as actionable tool errors. Existing tools SHALL be unchanged — the radius never gates `debug_end`.

#### Scenario: Radius returned through the tool
- **WHEN** `map_blast_radius` runs with a valid pattern covering the culprit
- **THEN** the result lists every daemon-recorded hit with file, line, and excerpt, plus the truncation flag

#### Scenario: Gate rejection surfaces verbatim
- **WHEN** the daemon rejects the pattern for missing the culprit file
- **THEN** the tool error carries the daemon's message naming that file

### Requirement: annotate_blast_radius tool
The MCP server SHALL register `annotate_blast_radius(session_id, annotations[])` with per-entry `{file, line, verdict, note?}`; verdict values SHALL be validated client-side against the enum before reaching the daemon, and daemon set-membership rejections SHALL surface as tool errors.

#### Scenario: Invalid verdict rejected client-side
- **WHEN** an annotation carries `verdict: "probably-fine"`
- **THEN** the call is rejected with the allowed values before reaching the daemon
