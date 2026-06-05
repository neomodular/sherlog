# mcp-server (delta)

## ADDED Requirements

### Requirement: debug_start accepts a title
`debug_start` SHALL accept an optional `title` parameter alongside the description and pass it to session creation. Callers omitting it SHALL keep working (daemon fallback derivation applies).

#### Scenario: Titled start
- **WHEN** `debug_start(title: "Cart total off by cents", description: "Symptom: ...")` is called
- **THEN** the session is created with that title and the response echoes it

#### Scenario: Legacy caller
- **WHEN** `debug_start` is called with only a description
- **THEN** the call succeeds and the response carries the derived fallback title

### Requirement: Titles in investigation payloads
`debug_resume` SHALL present the title as the case identity (description as detail), and the related-cases section of `debug_start` SHALL identify each recalled case by its title.

#### Scenario: Resume identifies by title
- **WHEN** `debug_resume` returns an investigation
- **THEN** the title appears as the case identity with the full description available separately
