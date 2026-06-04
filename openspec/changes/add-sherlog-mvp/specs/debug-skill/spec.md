# debug-skill

## ADDED Requirements

### Requirement: Detective loop discipline
The `/debug` skill SHALL drive the loop: gather bug context → `debug_start` → propose at least 3 hypotheses ("suspects") via `set_hypotheses` → place probes → `await_run` while the user reproduces → analyze run summary → kill/refine/split hypotheses with recorded evidence → iterate until one hypothesis is confirmed → apply fix → verify via a `fixed-check` run → cleanup. The skill SHALL NOT rely on conversation memory for investigation state; the daemon board is the source of truth.

#### Scenario: Initial investigation setup
- **WHEN** the user invokes `/debug` describing "login fails intermittently"
- **THEN** the skill starts a session, records at least 3 distinct hypotheses on the daemon board, and places probes before asking the user to reproduce

#### Scenario: Hypothesis killed by evidence
- **WHEN** a run summary shows probe p4 (discriminating for hypothesis h2) proved h2's premise false
- **THEN** the skill calls `update_hypothesis(h2, killed, …)` with an evidence note referencing the probe and run before continuing

### Requirement: Discriminating probes
Every hypothesis SHALL have at least one probe whose output discriminates it from rival hypotheses (not merely "execution reached here"), each placed as a single fire-and-forget HTTP line registered via `register_probe` and linked to its hypothesis.

#### Scenario: Probe placement
- **WHEN** the skill instruments for hypotheses h1 (race) and h2 (stale cache)
- **THEN** each probe is one line posting values that distinguish h1 from h2 (e.g. timestamps and cache age), inserted without new imports or wrappers wherever the language allows, and registered with file, line, and hypothesis

### Requirement: Reproduction interaction
The skill SHALL ask the user to reproduce the bug, block on `await_run`, then ask the user for the verdict and record it via `close_run`. On a zero-event await, the skill SHALL verify daemon connectivity before drawing any conclusion about hypotheses.

#### Scenario: Zero events after reproduction attempt
- **WHEN** `await_run` returns no events but the user says they reproduced the bug
- **THEN** the skill checks `/health` and probe placement (e.g. app needs restart/rebuild to pick up probes) instead of killing hypotheses

### Requirement: Fix verification via probes
After applying a fix, the skill SHALL run a verification cycle — user retests, run closed with verdict `fixed-check` — and SHALL confirm via probe evidence that the failure signature changed as predicted before declaring the bug solved.

#### Scenario: Fix confirmed
- **WHEN** the fix run's summary shows the previously-null token now populated and no error-path probe firing, and the user reports the bug no longer occurs
- **THEN** the skill marks the confirmed hypothesis, declares "elementary.", and proceeds to cleanup

### Requirement: Cleanup gate
The skill SHALL call `debug_end`, remove every listed probe from the code, then search the codebase for the session's probe URL fragment and require zero matches before reporting "case closed". If matches remain, the skill SHALL remove them and re-verify.

#### Scenario: Case closed only when clean
- **WHEN** `debug_end` lists 5 probes and the skill removes them
- **THEN** the skill greps for `2218/log/<session-id>` (and the `SHERLOG_PORT` variant if overridden), finds zero matches, and only then reports "case closed · all 5 probes removed"

### Requirement: Branded presentation
The skill SHALL print the sherlog banner at session start — the locked mascot sprite (exact Clawd glyphs plus the two-row inspector cap; navy cap and coral body when color is available) with the case status line — and SHALL use the detective vocabulary for state transitions ("the game is afoot" when awaiting reproduction, "elementary." on root cause, "case closed" after verified cleanup). The sprite art SHALL never vary between states; only status text changes.

#### Scenario: Session start banner
- **WHEN** a debug session starts with 3 hypotheses and 5 probes placed
- **THEN** the skill prints the mascot sprite with a status line of the form `sherlog · case #<id> · 3 suspects · 5 probes · watching :2218`

### Requirement: Session resumability
The skill SHALL support `/debug resume`, reconstructing the investigation from `debug_resume` and continuing the loop at the appropriate stage, and SHALL warn (not block) when `debug_start` reports another open session for the same directory.

#### Scenario: Resume next day
- **WHEN** the user runs `/debug resume` in a fresh session the next day
- **THEN** the skill restates the bug, surviving suspects, probe locations, and run history from daemon state and continues from where the investigation left off
