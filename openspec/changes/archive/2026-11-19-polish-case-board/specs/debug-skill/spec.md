# debug-skill (delta)

## ADDED Requirements

### Requirement: Statements stored without self-ID prefixes
The skill SHALL store hypothesis statements and evidence notes without leading self-identifiers ("h1: ...", "p2 -"); display naming is the UI's responsibility. References to *other* entities inside notes use their plain IDs (e.g. "p3 fired only in run 2"), which the UI upgrades to display names where it renders them.

#### Scenario: Clean statement authored
- **WHEN** the skill records the first hypothesis of a session
- **THEN** the stored statement is the bare claim ("race in token refresh"), with no "h1" prefix
