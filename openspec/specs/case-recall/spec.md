# case-recall Specification

## Purpose
Surface similar past cases when a new investigation starts, matched against closed cases' titles, root causes, and sibling patterns, so earlier findings inform the next hunt.

## Requirements

### Requirement: Similarity search over closed cases
The daemon SHALL search closed sessions by keyword similarity — tokenized, lowercased, stopword-stripped overlap between a query and each closed case's bug description, root cause, and confirmed hypothesis — returning the top 3 matches above a minimum score with session id, bug description, root cause, and fix summary.

#### Scenario: A similar solved case exists
- **WHEN** a search runs for "login fails intermittently after idle" and a closed case recorded root cause "token refresh race after idle timeout"
- **THEN** that case is returned with its root cause and fix summary

#### Scenario: Nothing relevant
- **WHEN** no closed case scores above the minimum threshold
- **THEN** the search returns an empty list (never weak matches padded to 3)

### Requirement: Recall surfaced at debug_start
The `debug_start` MCP tool SHALL include the similarity matches for the new bug description in its response, labeled as possibly related past cases, so the agent can use prior root causes as hypothesis leads — never as evidence.

#### Scenario: Starting a familiar-looking investigation
- **WHEN** `debug_start` is called with a description similar to a previously solved case
- **THEN** the response contains the matched case's root cause and fix summary alongside the new session id and probe template

### Requirement: Titles join the recall corpus
The recall similarity search SHALL include each closed case's title in the scored text (alongside description, root cause, and confirmed hypothesis), and recall results SHALL identify matches by title.

#### Scenario: Title tokens match
- **WHEN** a new investigation's text shares terms only with a closed case's title
- **THEN** that case can still surface as a match, identified by its title with root cause and fix summary attached

### Requirement: The sibling pattern joins the recall corpus
For solved cases carrying a blast radius, the radius pattern text SHALL be included in the text recall searches and scores, so a future investigation can match on the defect pattern itself. Hit file paths SHALL NOT be indexed.

#### Scenario: Recall by anti-pattern
- **WHEN** a past solved case stored the radius pattern `parseFloat\(.*price` and a new bug description mentions price parsing
- **THEN** recall can surface that case via the pattern text

#### Scenario: Hit paths not matchable
- **WHEN** a new description happens to mention a file path that was a hit in an old case's radius
- **THEN** that path alone does not produce a recall match
