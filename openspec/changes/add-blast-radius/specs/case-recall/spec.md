# case-recall (delta)

## ADDED Requirements

### Requirement: The sibling pattern joins the recall corpus
For solved cases carrying a blast radius, the radius pattern text SHALL be included in the text recall searches and scores, so a future investigation can match on the defect pattern itself. Hit file paths SHALL NOT be indexed.

#### Scenario: Recall by anti-pattern
- **WHEN** a past solved case stored the radius pattern `parseFloat\(.*price` and a new bug description mentions price parsing
- **THEN** recall can surface that case via the pattern text

#### Scenario: Hit paths not matchable
- **WHEN** a new description happens to mention a file path that was a hit in an old case's radius
- **THEN** that path alone does not produce a recall match
