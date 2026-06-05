# case-recall (delta)

## ADDED Requirements

### Requirement: Titles join the recall corpus
The recall similarity search SHALL include each closed case's title in the scored text (alongside description, root cause, and confirmed hypothesis), and recall results SHALL identify matches by title.

#### Scenario: Title tokens match
- **WHEN** a new investigation's text shares terms only with a closed case's title
- **THEN** that case can still surface as a match, identified by its title with root cause and fix summary attached
