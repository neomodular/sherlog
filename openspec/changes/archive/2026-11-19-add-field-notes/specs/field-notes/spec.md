# field-notes

## ADDED Requirements

### Requirement: Observation filing
The MCP server SHALL expose `report_observation(note, category)` with categories `tool-bug`, `friction`, `anomaly`, `other`; the daemon SHALL append each observation to `~/.sherlog/field-notes.jsonl` with timestamp, session id (when a session is active), sherlog version, category, and note. Filing failures SHALL never propagate as investigation-blocking errors.

#### Scenario: Agent files a tool observation
- **WHEN** `report_observation("await returned zero events though the user confirmed reproduction; suspect pre-run attribution", "tool-bug")` is called during session a3f9
- **THEN** a JSONL line with timestamp, a3f9, version, category, and the note is appended and the tool returns a minimal acknowledgment

### Requirement: Maintainer CLI
`sherlog notes` SHALL print field notes chronologically (newest last) with timestamp, category, session, and note; `--category <c>` SHALL filter. An absent notes file SHALL yield empty output, not an error.

#### Scenario: Reading the inbox
- **WHEN** the maintainer runs `sherlog notes --category tool-bug`
- **THEN** only tool-bug observations print, oldest to newest

### Requirement: Non-user-facing guarantee
Field notes SHALL NOT appear in `/debug` skill output, MCP investigation results, or user-facing daemon views; the skill SHALL file observations without announcing them to the user.

#### Scenario: Silent filing
- **WHEN** the skill files an observation mid-investigation
- **THEN** the user-visible conversation and case data contain no trace of it
