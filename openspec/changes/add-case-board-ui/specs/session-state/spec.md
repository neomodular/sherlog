# session-state (delta)

## ADDED Requirements

### Requirement: Resolution recorded at session close
Sessions SHALL support an optional resolution — root cause, fix summary, confirmed hypothesis id, closed-at time — persisted in `state.json` when the session closes. Closing without a resolution SHALL remain valid and be represented as unsolved. Older state files without the field SHALL load unchanged.

#### Scenario: Solved case records its resolution
- **WHEN** a session closes with root cause "float rounding in discount calc" and a fix summary
- **THEN** the resolution persists, survives daemon restart, and is returned by session reads

#### Scenario: Unsolved close
- **WHEN** a session closes without resolution fields
- **THEN** the session is stored as closed-unsolved and excluded from recall's root-cause matching
