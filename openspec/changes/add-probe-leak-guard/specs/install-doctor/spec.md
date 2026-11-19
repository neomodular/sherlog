# install-doctor

## Purpose

Give users and agents one read-only command, `sherlog doctor`, that checks the whole sherlog install chain and reports each link as healthy, broken, or not applicable — replacing manual walks through the troubleshooting guide.

## ADDED Requirements

### Requirement: Doctor is read-only
`sherlog doctor` SHALL NOT create, modify, or delete any file, SHALL NOT spawn or stop the daemon, and SHALL make no network request other than a loopback `GET /health` to the configured port.

#### Scenario: No daemon is spawned
- **WHEN** no daemon is running and the user runs `sherlog doctor`
- **THEN** the daemon row reports it as not running and no daemon process is started

### Requirement: One aligned row per check with a fixed status vocabulary
Doctor SHALL print one row per check with the check name, a status of exactly `ok`, `!!`, or `--` (not applicable or informational), and a one-line detail. Every `!!` row SHALL name the repair.

#### Scenario: Broken check names its fix
- **WHEN** the storage root is not writable
- **THEN** its row shows `!!` and the detail names the path and the permission to fix

### Requirement: Exit status reflects failures only
Doctor SHALL exit 1 when any row is `!!` and 0 otherwise; `--` rows SHALL NOT affect the exit status.

#### Scenario: Not-applicable rows pass
- **WHEN** doctor runs outside a git repository and every other check is `ok`
- **THEN** the guard row is `--` and the exit status is 0

### Requirement: Doctor covers the install chain
Doctor SHALL check at least: the running binary's version; that the `sherlog` resolved on `PATH` is the running binary; that the storage root exists or can be created and is writable; that every session's state file parses; that the effective configuration is valid; the daemon (not running → `--`, running same version → `ok`, different version → `--` noting it will restart on its own, port held by a non-sherlog process → `!!`); and the leak guard in the current repository.

#### Scenario: PATH shadowing detected
- **WHEN** the `sherlog` on `PATH` is a different file from the binary running doctor
- **THEN** the PATH row shows `!!` naming both paths

#### Scenario: Corrupt session reported
- **WHEN** one session's `state.json` does not parse
- **THEN** the sessions row shows `!!` naming that session ID, and the remaining checks still run

#### Scenario: Foreign port holder
- **WHEN** a process that is not the sherlog daemon answers on the configured port
- **THEN** the daemon row shows `!!` and points to the port override setting

### Requirement: Doctor reports leftover probes from closed cases
Doctor SHALL report unremoved probes belonging to closed sessions as `!!` with their count and the `sherlog probes --stale` command, and SHALL report unremoved probes of open sessions as informational only, since an investigation in progress legitimately has live probes.

#### Scenario: Probes left by a closed case
- **WHEN** a closed session still lists two unremoved probes
- **THEN** the probes row shows `!!` with the count and the command that lists them

#### Scenario: Live investigation is not a failure
- **WHEN** the only unremoved probes belong to an open session
- **THEN** the probes row is not `!!`
