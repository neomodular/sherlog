# debug-skill (delta)

## ADDED Requirements

### Requirement: Skill honors verbosity and color preferences
`debug_start` SHALL deliver `preferences {verbosity, color}` and the skill SHALL honor them: `detective` (default) keeps the mascot banner and vocabulary; `minimal` drops all theming and prints plain status lines while keeping every loop obligation (≥3 hypotheses, discriminating probes, verdicts, cleanup gate, functional lines like the cleanup result) unchanged. `color: never` SHALL strip ANSI sequences.

#### Scenario: Minimal mode stays rigorous
- **WHEN** verbosity is `minimal` and a session runs start → fix → cleanup
- **THEN** no mascot art or detective phrases are printed, but hypotheses, probe registration, verdicts, and the zero-grep cleanup confirmation all still occur and are reported plainly

#### Scenario: Default unchanged
- **WHEN** no config file exists
- **THEN** the skill behaves exactly as the MVP detective presentation
