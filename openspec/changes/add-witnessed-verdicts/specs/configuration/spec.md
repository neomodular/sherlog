# configuration (delta)

## ADDED Requirements

### Requirement: Verdict source mode is configurable
The configuration SHALL include `verdicts` with values `agent` (default) or `witnessed`, settable through the config file and `sherlog config set` like the other non-port keys, listed with its source by `sherlog config list`, and validated so any other value is refused.

#### Scenario: Turning witnessed mode on
- **WHEN** the user runs `sherlog config set verdicts witnessed`
- **THEN** the setting is stored and subsequent agent run closes are refused

#### Scenario: Invalid value refused
- **WHEN** the user runs `sherlog config set verdicts maybe`
- **THEN** the command fails naming `agent` and `witnessed` and the config file is unchanged
