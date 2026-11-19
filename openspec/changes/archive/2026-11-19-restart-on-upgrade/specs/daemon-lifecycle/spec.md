# daemon-lifecycle (delta)

## ADDED Requirements

### Requirement: The daemon detects replacement of its own binary
At startup the daemon SHALL record the identity of its own executable (device/inode where available, plus mtime and size) and SHALL re-check it on a fixed background interval. A vanished file or a changed identity SHALL be treated as "a new version landed". Version strings SHALL NOT be compared. A failure to resolve the executable path at startup SHALL disable the watcher for that process with a single log line, never a fatal error.

#### Scenario: Replaced binary triggers
- **WHEN** the file at the daemon's executable path is atomically replaced (rename-over) while the daemon runs
- **THEN** within one watch interval the daemon begins draining toward exit

#### Scenario: Deleted binary triggers
- **WHEN** the executable file is removed (as brew cleanup does to the old Cellar path)
- **THEN** the daemon begins draining toward exit

#### Scenario: Untouched binary never triggers
- **WHEN** the executable is never modified
- **THEN** the daemon keeps running indefinitely with no drain

### Requirement: The daemon drains before exiting
On trigger the daemon SHALL mark itself draining and exit cleanly once no `await_run` wait is in flight, checked via an in-flight gauge. Awaits arriving during the drain SHALL still be served. If draining exceeds the maximum await timeout, the daemon SHALL exit regardless. The exit SHALL be a clean return through the normal shutdown path (exit code 0) after one log line naming the reason.

#### Scenario: In-flight await completes first
- **WHEN** the trigger fires while an `await_run` is blocking
- **THEN** the daemon exits only after that await returns to its caller

#### Scenario: Bounded fallback
- **WHEN** the drain has lasted longer than the maximum await timeout
- **THEN** the daemon exits even though an await is still in flight

### Requirement: The swap completes without user action or data loss
After the drained exit, the next MCP tool call SHALL auto-spawn the binary currently on disk (the existing silent-port spawn path), and the investigation SHALL continue from replayed state: open runs replay as open and `await_run` re-attaches. No investigation data SHALL be lost across the swap.

#### Scenario: Upgrade mid-investigation
- **WHEN** the binary is upgraded while a case has an open run, the daemon drains out, and the next tool call arrives
- **THEN** the new binary is serving, the session and its open run are intact, and `await_run` re-attaches to that run
