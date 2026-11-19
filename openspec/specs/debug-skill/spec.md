# debug-skill Specification

## Purpose
Define the /debug detective loop the agent follows — suspects, discriminating probes, user reproduction, evidence-based elimination, verified fix, and guaranteed cleanup — reasoning only from the daemon's board, never from conversation memory.

## Requirements

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

### Requirement: Skill records resolutions
At case close after a confirmed fix, the `/debug` skill SHALL pass `root_cause`, `fix_summary`, and the confirmed hypothesis id to `debug_end` so the case becomes recall material. When closing unsolved, the skill SHALL say so plainly and close without fabricated resolution fields.

#### Scenario: Solved case feeds the archive
- **WHEN** the loop confirms a root cause and the fix verifies
- **THEN** `debug_end` is called with a concise root cause and fix summary before "case closed" is reported

### Requirement: Skill uses recalled cases as leads
When `debug_start` returns related past cases, the skill SHALL consider them while forming hypotheses and cite them when used ("similar to case #b2c1: float rounding"), but SHALL never kill or confirm a hypothesis on recall alone — probes remain the only evidence.

#### Scenario: Prior root cause informs a suspect
- **WHEN** a related case's root cause plausibly matches the new symptom
- **THEN** the skill includes a hypothesis derived from it, marked with the source case, and still validates it with a discriminating probe

### Requirement: Skill mentions the Case Board
When a session starts, the skill SHALL tell the user the investigation is observable live at the daemon URL (e.g. `http://127.0.0.1:2218`), once, as part of the banner block.

#### Scenario: Banner includes the board link
- **WHEN** `/debug` prints the case-opened banner
- **THEN** it includes the local Case Board URL for live observation

### Requirement: Skill honors verbosity and color preferences
`debug_start` SHALL deliver `preferences {verbosity, color}` and the skill SHALL honor them: `detective` (default) keeps the mascot banner and vocabulary; `minimal` drops all theming and prints plain status lines while keeping every loop obligation (≥3 hypotheses, discriminating probes, verdicts, cleanup gate, functional lines like the cleanup result) unchanged. `color: never` SHALL strip ANSI sequences.

#### Scenario: Minimal mode stays rigorous
- **WHEN** verbosity is `minimal` and a session runs start → fix → cleanup
- **THEN** no mascot art or detective phrases are printed, but hypotheses, probe registration, verdicts, and the zero-grep cleanup confirmation all still occur and are reported plainly

#### Scenario: Default unchanged
- **WHEN** no config file exists
- **THEN** the skill behaves exactly as the MVP detective presentation

### Requirement: Skill files observations on sherlog misbehavior
The skill SHALL call `report_observation` whenever sherlog itself behaves unexpectedly — e.g. zero events despite a confirmed reproduction, await/debounce oddities, cleanup-gate surprises, tool errors — choosing the fitting category, then SHALL continue the investigation normally without surfacing the note to the user. Difficulties with the user's bug are NOT observations; only tool behavior is.

#### Scenario: Zero-event anomaly filed
- **WHEN** an await returns zero events but the user confirms the bug reproduced and /health is fine
- **THEN** the skill files a `tool-bug`/`anomaly` observation describing the discrepancy and proceeds with its connectivity/rebuild checks as usual

#### Scenario: Hard bugs are not noise
- **WHEN** an investigation is merely difficult (hypotheses keep dying, bug is elusive)
- **THEN** no observation is filed

### Requirement: Skill interprets adopted evidence
The skill SHALL treat adopted events as valid evidence while acknowledging the label: when a run's evidence is entirely adopted and the verdict carries weight (notably fixed-check verification), the skill SHALL sanity-check consistency (expected probes present, values plausible) and request one live reproduction if anything is inconsistent — never silently discounting, never blindly trusting.

#### Scenario: Fixed-check on fully adopted evidence
- **WHEN** a fixed-check run's summary is entirely adopted and matches the predicted post-fix signature
- **THEN** the skill accepts it as verification, noting the attribution was inferred

#### Scenario: Adopted evidence looks inconsistent
- **WHEN** adopted events are present but a discriminating probe expected for the reproduction is absent
- **THEN** the skill asks the user to reproduce once more while the run is open instead of concluding

### Requirement: Skill authors the title
At `debug_start` the skill SHALL author a title: an imperative or noun-phrase summary of the failure, at most ~60 characters, specific to the observed problem (e.g. "Login 401 after idle timeout" — never "Bug in auth" or a full sentence of narrative). The skill banner status line SHALL show the title.

#### Scenario: Title authored from a rambling report
- **WHEN** the user reports a multi-paragraph bug story
- **THEN** the skill distills a ≤60-char specific title and starts the session with it, and the banner shows the title

### Requirement: Soft-structured description
The skill SHALL write the description as plain text under the headings `Symptom:`, `Expected:`, `Repro:`, `Context:` — including only headings it has real content for, quoting exact error text in Symptom when available, and never inventing Expected/Repro the user did not state. The skill SHALL ask the user for clarification only when the symptom or expected behavior is genuinely unclear; otherwise it proceeds.

#### Scenario: Partial information stays honest
- **WHEN** the user describes a symptom and expected behavior but no reproduction steps
- **THEN** the description contains Symptom and Expected sections only — no fabricated Repro

#### Scenario: Unclear symptom triggers one question
- **WHEN** the user's report does not establish what actually goes wrong
- **THEN** the skill asks a clarifying question before debug_start instead of guessing

### Requirement: Statements stored without self-ID prefixes
The skill SHALL store hypothesis statements and evidence notes without leading self-identifiers ("h1: ...", "p2 -"); display naming is the UI's responsibility. References to *other* entities inside notes use their plain IDs (e.g. "p3 fired only in run 2"), which the UI upgrades to display names where it renders them.

#### Scenario: Clean statement authored
- **WHEN** the skill records the first hypothesis of a session
- **THEN** the stored statement is the bare claim ("race in token refresh"), with no "h1" prefix

### Requirement: The radius is mapped after confirm, before the fix
Once a hypothesis is confirmed, the skill SHALL propose a sibling pattern and call `map_blast_radius` **before applying the fix**, while the anti-pattern still exists at the culprit site. The pattern SHALL target the defect mechanism (the anti-pattern), not the symptom text.

#### Scenario: Search precedes the fix
- **WHEN** the root cause "float rounding in discount calc" is confirmed
- **THEN** the skill maps the radius for the rounding anti-pattern before editing any file

### Requirement: Every hit is graded honestly
The skill SHALL annotate the hits via `annotate_blast_radius`, grading each `sibling-bug`, `safe`, or `already-covered` from reading the site — `safe` is a legitimate verdict, and hits it cannot judge SHALL be left `unreviewed` and said so to the user, never guessed.

#### Scenario: Unjudgeable hit left unreviewed
- **WHEN** a hit sits in generated code the skill cannot evaluate
- **THEN** it remains `unreviewed` and the skill tells the user why

### Requirement: No unrecorded coverage claims
The skill SHALL NOT assert that sibling occurrences were checked unless a recorded radius exists for the session; when reporting, it SHALL cite the radius (hit and unreviewed counts, truncation) rather than memory. If the pattern is rejected for missing the culprit, the skill SHALL refine the pattern — never restate coverage in prose.

#### Scenario: Coverage stated from the board
- **WHEN** the user asks whether the bug exists elsewhere
- **THEN** the skill answers from the recorded radius ("7 hits: 2 sibling bugs, 4 safe, 1 unreviewed") or runs the search first — never from recollection

### Requirement: Sibling bugs inform the close, not gate it
Hits graded `sibling-bug` SHALL be reported to the user before `debug_end` (fix now or track separately — the user decides). The skill SHALL NOT block or delay the close on an unmapped or partially reviewed radius.

#### Scenario: Sibling bugs surfaced at close
- **WHEN** the radius holds 2 `sibling-bug` hits at `debug_end` time
- **THEN** the skill lists both sites and asks the user how to proceed before closing

### Requirement: Discriminating probes are registered with their prediction pair
The skill SHALL supply `expected_if_true` / `expected_if_false` when registering each hypothesis's discriminating probe, stating concretely how the payload differs under each outcome. Plain path-tracer probes MAY omit predictions, but every hypothesis SHALL have at least one predicted probe before the reproduction wait begins.

#### Scenario: Predicted probe authored per suspect
- **WHEN** the skill plants the discriminating probe for "race between token refresh and request"
- **THEN** it registers the probe with distinct `expected_if_true` and `expected_if_false` payload descriptions

### Requirement: Kills and confirms cite their evidence structurally
The skill SHALL pass `probe_id` and `run_id` on every `killed`/`confirmed` transition, citing the closed run whose summary it is reasoning from, and keep the free-text note as the human-readable explanation of the same evidence.

#### Scenario: Kill carries the citation
- **WHEN** the skill kills a suspect because `p4` showed a fresh cache in run `r2`
- **THEN** it calls `update_hypothesis` with `probe_id: "p4"`, `run_id: "r2"`, and a note explaining the payload

### Requirement: The fix prediction is recorded before the fixed-check reproduction
Before asking the user to retest a fix, the skill SHALL pass its evidence-change prediction as `await_run(prediction=...)` — the board, not the conversation, holds the claim the fixed-check is judged against. When `close_run(fixed-check)` is rejected for a missing prediction, the skill SHALL re-await with the prediction and ask for one more reproduction rather than restating the prediction in prose.

#### Scenario: Prediction travels through the tool
- **WHEN** the skill expects "p1's token populated; p5 fires zero times" after a fix
- **THEN** it supplies exactly that as the `prediction` parameter of the fixed-check `await_run`

### Requirement: Gate rejections are repaired, never routed around
When the daemon rejects a transition (board too small, missing citation, no reproduced run, prediction-less confirm, invalid solved close), the skill SHALL treat the rejection as a discipline breach: perform the named repair (extend the board, register the predicted probe, run again, fix the resolution) and retry the transition. It SHALL NOT weaken the claim to fit a lenient path, close unsolved to bypass a failed solved close the user believes is solved, or retry the identical call.

#### Scenario: Rejected confirm repaired
- **WHEN** a confirm is rejected because the citing probe carries no predictions
- **THEN** the skill registers a predicted probe for that hypothesis, reruns the reproduction, and confirms citing the new evidence

### Requirement: Repro rate is reported, not asserted
The skill SHALL state determinism from the computed repro rate returned by the tools (e.g. "reproduced 2/3 runs — intermittent") and SHALL NOT assert always/intermittent from memory. For an intermittent bug, absence of a probe's events in a single `not-reproduced` run SHALL NOT kill a suspect on its own.

#### Scenario: Intermittency stated from the computed rate
- **WHEN** the board shows repro rate 2/5 after several runs
- **THEN** the skill reports the bug as intermittent citing 2/5, and keeps gathering runs rather than killing suspects from one quiet run

### Requirement: Prevention references are recorded only when real
When a regression test or guardrail actually exists at close time, the skill SHALL pass `regression_test_ref` / `guardrail` to `debug_end`. It SHALL NOT invent references — a solved close with no prevention artifact simply omits them.

#### Scenario: Real test referenced
- **WHEN** the fix landed with a regression test `TestRefreshRace`
- **THEN** the skill closes with `regression_test_ref: "TestRefreshRace"`

#### Scenario: No artifact, no reference
- **WHEN** no regression test was written
- **THEN** the skill closes solved without the field rather than fabricating one
