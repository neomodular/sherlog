# session-state (delta)

## ADDED Requirements

### Requirement: Sessions carry a title
Sessions SHALL store an optional short title alongside the description, persisted in `state.json`. When a session is created without a title, the daemon SHALL derive one at read time from the description (first ~60 characters, word-boundary truncated with an ellipsis) so every session payload carries a non-empty title. Legacy state files without the field SHALL load unchanged.

#### Scenario: Titled session round-trips
- **WHEN** a session is created with title "Login 401 after idle timeout" and a detailed description
- **THEN** both persist, survive daemon restart, and are returned distinctly in session reads

#### Scenario: Legacy session gets a derived title
- **WHEN** a pre-title `state.json` session with a 300-character description is loaded and read
- **THEN** its payload carries a word-boundary-truncated ~60-char title ending in an ellipsis, and the stored file is not rewritten
