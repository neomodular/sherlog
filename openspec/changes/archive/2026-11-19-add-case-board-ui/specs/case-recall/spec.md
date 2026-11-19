# case-recall

## ADDED Requirements

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
