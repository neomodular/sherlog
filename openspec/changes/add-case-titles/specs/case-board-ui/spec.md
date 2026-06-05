# case-board-ui (delta)

## ADDED Requirements

### Requirement: Titles in lists, structured description in detail
The Cases list (and any case reference in the UI, including stale-probes rows and diff headers) SHALL display the session title. The case detail view SHALL show the title as the header with the full description below, rendering `Symptom:` / `Expected:` / `Repro:` / `Context:` leading lines with bold emphasis when present.

#### Scenario: Scannable case list
- **WHEN** the Cases view lists a session titled "Login 401 after idle timeout" with a long description
- **THEN** the list row shows the title (not the description), and opening the case shows title header plus the description with its headings emphasized

#### Scenario: Legacy session display
- **WHEN** a pre-title session appears in the list
- **THEN** its derived truncated title displays and the detail view shows the original description unmodified
