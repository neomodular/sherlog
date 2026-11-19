# configuration

## ADDED Requirements

### Requirement: Config file with strict schema
Sherlog SHALL read `~/.sherlog/config.json` at daemon startup supporting exactly: `port`, `flood_keep`, `await_debounce_seconds`, `await_max_timeout_seconds`, `retention_days`, `verbosity`, `color`. Unknown keys SHALL fail loading with a clear error; an absent file SHALL yield built-in defaults (current MVP behavior).

#### Scenario: No config file
- **WHEN** the daemon starts and no config.json exists
- **THEN** all effective values equal the built-in defaults and behavior matches the MVP

#### Scenario: Typo in config
- **WHEN** config.json contains `"flod_keep": 50`
- **THEN** loading fails with an error naming the unknown key

### Requirement: Precedence env > file > default
Environment overrides (`SHERLOG_PORT`) SHALL take precedence over config-file values, which SHALL take precedence over defaults; resolution SHALL happen once at startup into the effective configuration used everywhere.

#### Scenario: Env wins over file
- **WHEN** config.json sets port 3000 and SHERLOG_PORT=4000
- **THEN** the daemon listens on 4000

### Requirement: Config CLI
`sherlog config list` SHALL print every key's effective value and its source (default/file/env); `sherlog config get <key>` SHALL print one value; `sherlog config set <key> <value>` SHALL validate (known key, parseable value, range: flood_keep 1–1000, debounce 0–30, max timeout 30–3600, retention_days ≥ 0, verbosity detective|minimal, color auto|always|never) and write the file atomically.

#### Scenario: Setting a knob
- **WHEN** `sherlog config set flood_keep 50` runs
- **THEN** config.json contains 50, and `sherlog config list` shows flood_keep 50 (source: file)

#### Scenario: Rejecting an invalid value
- **WHEN** `sherlog config set verbosity loud` runs
- **THEN** the command fails listing the allowed values and the file is unchanged

### Requirement: Knobs drive daemon behavior
The effective `flood_keep`, `await_debounce_seconds`, and `await_max_timeout_seconds` SHALL replace the corresponding hardcoded values in flood control and the await engine, with current defaults (20 / 2 / 600) unchanged.

#### Scenario: Bigger flood window
- **WHEN** flood_keep is 50 and a probe fires 1,000 times in a run
- **THEN** the first 50 and last 50 events are retained with an exact total of 1,000

### Requirement: Retention pruning
With `retention_days > 0`, the daemon SHALL delete sessions closed more than N days ago (disk and memory) at startup and every 24 hours, logging what was pruned. Open sessions SHALL never be pruned. The default SHALL be 0 (keep forever).

#### Scenario: Old closed case pruned
- **WHEN** retention_days is 30 and a session closed 45 days ago
- **THEN** the next prune removes it and logs its id

#### Scenario: Open session immune
- **WHEN** retention_days is 1 and a session has been open for 10 days
- **THEN** it is not pruned

### Requirement: Effective config is observable
`GET /health` SHALL include the effective configuration (values + sources) so users and the skill can diagnose behavior.

#### Scenario: Checking why awaits end early
- **WHEN** a user GETs /health after setting await_debounce_seconds to 5
- **THEN** the response shows the effective debounce of 5 sourced from the file
