# configuration (delta)

## ADDED Requirements

### Requirement: Abandonment threshold is configurable
The configuration SHALL include `abandon_after_days`, a positive integer defaulting to 7, settable through the config file like the other non-port keys. It SHALL be listed by `sherlog config list` with its source and validated by `config set`.

#### Scenario: Set through the CLI
- **WHEN** the user runs `sherlog config set abandon_after_days 14`
- **THEN** stats classify an open case as abandoned only after 14 days without a record

#### Scenario: Invalid value refused
- **WHEN** the user runs `sherlog config set abandon_after_days 0`
- **THEN** the command fails naming the allowed range and the config file is unchanged
