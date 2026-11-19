# debug-skill (delta)

## ADDED Requirements

### Requirement: Skill interprets adopted evidence
The skill SHALL treat adopted events as valid evidence while acknowledging the label: when a run's evidence is entirely adopted and the verdict carries weight (notably fixed-check verification), the skill SHALL sanity-check consistency (expected probes present, values plausible) and request one live reproduction if anything is inconsistent — never silently discounting, never blindly trusting.

#### Scenario: Fixed-check on fully adopted evidence
- **WHEN** a fixed-check run's summary is entirely adopted and matches the predicted post-fix signature
- **THEN** the skill accepts it as verification, noting the attribution was inferred

#### Scenario: Adopted evidence looks inconsistent
- **WHEN** adopted events are present but a discriminating probe expected for the reproduction is absent
- **THEN** the skill asks the user to reproduce once more while the run is open instead of concluding
