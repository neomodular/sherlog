# case-board-ui (delta)

## ADDED Requirements

### Requirement: The case detail renders the blast radius
When a session carries a radius, the case detail (and closed-case view) SHALL render the pattern, each hit with file, line, excerpt, and a verdict badge (`sibling-bug` / `safe` / `already-covered` / `unreviewed`), plus the unreviewed count and a visible truncation notice when `truncated` is set. Rendering SHALL remain GET-only with no external origins; hits SHALL display as inert text, never as links that fetch or mutate.

#### Scenario: Radius section rendered with badges
- **WHEN** the detail view shows a case with a 4-hit radius, one hit graded `sibling-bug`
- **THEN** the pattern and all 4 hits render with their badges and the unreviewed count

#### Scenario: Truncation visible
- **WHEN** the stored radius has `truncated: true`
- **THEN** the section shows a notice that the hit cap was reached

#### Scenario: Long hit lists collapse
- **WHEN** the radius holds more hits than the preview cap
- **THEN** graded hits and the first preview rows render, and the remainder folds behind a native expander labeled with the hidden count — no script handler, content still inert text

#### Scenario: No radius, no section
- **WHEN** a case has no blast radius
- **THEN** the detail view omits the section entirely
