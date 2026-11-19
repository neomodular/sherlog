# case-audit

## Purpose

Keep an append-only, metadata-only history of every governance event in every case — transitions and refusals alike — that outlives session retention, so outcomes can be measured and any failure traced to the step where it happened.

## ADDED Requirements

### Requirement: Every governance event is recorded
The daemon SHALL append one audit record for each accepted state change — case opened, case closed (solved or unsolved), suspects set, hypothesis status change, probe registered, probe removed, run opened, run closed, blast radius mapped, blast radius annotated — after the change is persisted.

#### Scenario: A kill is recorded with its citation
- **WHEN** the agent kills `h2` citing probe `p2` in run `r1`
- **THEN** one record of kind `hypothesis` is appended naming the case, `h2`, status `killed`, `p2`, and `r1`

#### Scenario: A run close records its verdict
- **WHEN** run `r3` closes with verdict `fixed-check`
- **THEN** one record of kind `run_closed` names `r3`, the verdict, and whether the run carried a prediction

### Requirement: Every gate refusal is recorded with a stable code
When a store gate refuses a call, the daemon SHALL append a record of kind `refused` naming the case, the tool, and a stable kebab-case gate code unique to that gate (for example `insufficient-hypotheses`, `evidence-citation-required`). Each gate SHALL map to exactly one code, and codes SHALL NOT change once released.

#### Scenario: Uncited confirm refused
- **WHEN** a confirm without a citation is refused
- **THEN** a `refused` record names `update_hypothesis` and the code `evidence-citation-required`

#### Scenario: Lookup errors are not refusals
- **WHEN** a call names an unknown session
- **THEN** no `refused` record is appended

### Requirement: Records carry the stage and version
Each record SHALL carry a schema version, a UTC timestamp, the case ID, the event kind, the case-next stage evaluated immediately before the event, and the sherlog version that wrote it.

#### Scenario: Refusal pinned to its stage
- **WHEN** a confirm is refused while the case is in the `judge` stage
- **THEN** the `refused` record carries stage `judge`

### Requirement: Records hold metadata only
Audit records SHALL contain only identifiers, codes, enumerated values, counts, booleans, and timestamps. They SHALL NOT contain case titles or descriptions, hypothesis statements, notes, predictions, probe payloads, file paths, or resolution text.

#### Scenario: A suspect set records a count
- **WHEN** the agent sets a board of four suspects
- **THEN** the record carries the four suspect IDs and no statement text

#### Scenario: Registered probe without its path
- **WHEN** probe `p3` is registered for `h1` with a prediction pair
- **THEN** the record names `p3`, `h1`, and that a prediction is present, and no file path or prediction text

### Requirement: The log is append-only and outlives retention
The audit log SHALL be a single append-only file under the storage root, never rewritten or truncated by sherlog. Retention pruning SHALL NOT delete or alter audit records of pruned sessions.

#### Scenario: Pruned case keeps its history
- **WHEN** retention prunes a case closed 40 days ago
- **THEN** the case's audit records remain and `sherlog audit --session <id>` still prints its timeline

### Requirement: Session state stays authoritative
The audit record SHALL be appended only after the state change it describes is persisted; a failure to append SHALL NOT fail, roll back, or delay the tool call, and SHALL be reported once on the daemon's stderr.

#### Scenario: Unwritable audit log
- **WHEN** the audit file cannot be written and the agent registers a probe
- **THEN** the probe is registered, the tool call succeeds, and the daemon logs the audit failure

### Requirement: Existing cases are backfilled once
When the audit log does not exist at daemon start, the daemon SHALL synthesize records for every existing session from its persisted state — case opened and closed, final hypothesis statuses, probes, runs, blast radius — each marked `backfilled`. Backfill SHALL run only when the file is absent, and SHALL NOT synthesize refusals.

#### Scenario: First start after upgrade
- **WHEN** the daemon starts with three existing sessions and no audit log
- **THEN** the log is created holding backfilled records for those three cases and no `refused` records

#### Scenario: Backfill is not repeated
- **WHEN** the daemon restarts and the audit log exists
- **THEN** no backfill records are appended

### Requirement: Readers tolerate damaged lines
Readers SHALL skip lines that do not parse or carry an unknown schema version or kind, and SHALL report how many were skipped rather than failing.

#### Scenario: Truncated last line
- **WHEN** the final line of the log is half-written after a crash
- **THEN** `sherlog audit` prints every complete record and reports one skipped line

### Requirement: The audit CLI prints a case timeline without a daemon
`sherlog audit [--session ID] [--json]` SHALL read the log directly, print the chosen case's records in order (default: the most recently opened case in the log) with refusals visually distinct, and with `--json` print the records as JSON lines.

#### Scenario: Timeline with a refusal
- **WHEN** a case had a refused confirm followed by an accepted one
- **THEN** the timeline shows the refusal with its gate code before the accepted confirm
