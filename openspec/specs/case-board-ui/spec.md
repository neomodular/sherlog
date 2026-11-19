# case-board-ui Specification

## Purpose
Give the user a read-only, daemon-served web view of investigations — case list and detail, live evidence, run comparison, stale probes, and daemon health — so the state the agent reasons from is visible to the person too.

## Requirements

### Requirement: Case Board served by the daemon
The daemon SHALL serve a browser UI at `GET /` on its existing listener, with all assets embedded in the binary (`go:embed`) and zero requests to external origins.

#### Scenario: Open the Case Board
- **WHEN** a user browses to `http://127.0.0.1:2218/` while the daemon runs
- **THEN** the Case Board loads fully from embedded assets with no external network requests

### Requirement: Case list and case detail views
The UI SHALL list all sessions (open first, then closed with their resolution summaries) and SHALL show per-case detail: bug description, hypothesis board (statement, status, evidence notes), probe registry (id, file:line, linked hypothesis, removed flag), and run history (verdicts, per-probe counts).

#### Scenario: Browsing a closed case
- **WHEN** the user opens a closed case that recorded a resolution
- **THEN** the detail view shows the root cause, fix summary, confirmed hypothesis, and the full board/probe/run history as the investigation left them

### Requirement: Live evidence tail
The UI SHALL stream new events for an open session via SSE (`/api/events?session=<id>`) — log events, board changes, run open/close, probe changes — appearing in the case detail without reload, honoring flood-control truncation.

#### Scenario: Watching a reproduction live
- **WHEN** the user has the case open in a browser while probes fire during a run
- **THEN** events appear in the evidence tail as they arrive and the board reflects hypothesis updates without a page reload

#### Scenario: Slow browser does not block the daemon
- **WHEN** an SSE subscriber stops reading (full buffer or closed connection)
- **THEN** the daemon drops that subscriber without delaying ingest or other subscribers

### Requirement: Run comparison view
The UI SHALL provide a side-by-side comparison of any two runs of a case using the run-diff data, pinning divergent probes (fired in only one run, or large count divergence) to the top and badging flood-truncated probes.

#### Scenario: Comparing failing vs fixed-check runs
- **WHEN** the user selects run 1 (reproduced) and run 3 (fixed-check) in the comparison picker
- **THEN** probes are shown side by side with counts and sample values from each run, divergent probes first

### Requirement: Stale probes view
The UI SHALL show all registered-but-not-removed probes across all sessions with session, file, and line — the browser equivalent of `sherlog probes --stale`.

#### Scenario: Spotting leftovers
- **WHEN** any session (open or closed) has probes not marked removed
- **THEN** the stale probes view lists each with enough information to locate and delete the probe line

### Requirement: Read-only UI
Browser-facing routes SHALL be read-only; no UI interaction SHALL mutate sessions, hypotheses, probes, runs, or logs. Mutations remain exclusive to the MCP/internal API path.

#### Scenario: No mutation surface
- **WHEN** the UI is exercised end to end
- **THEN** every request it issues is a GET (page, data, SSE) and daemon state is byte-identical afterward

### Requirement: Health view
The Case Board SHALL provide a Health view (`#/health`, linked from the main navigation) rendering /api/stats as: a status header (mascot + "on the case" when all self-checks pass; the failing check's detail in plain text otherwise), vitals with live-ticking uptime, effective config with per-key sources, storage panel (data path, disk usage, session counts, total events, field-notes count), activity panel (last event, trailing-hour events, SSE subscribers, open run), and a stale-probes count linking to the Stale Probes view.

#### Scenario: Healthy daemon at a glance
- **WHEN** the user opens #/health on a healthy daemon
- **THEN** the header shows the mascot with "on the case" and every panel renders from /api/stats without external requests

#### Scenario: Failing self-check surfaces
- **WHEN** a self-check reports ok:false
- **THEN** the header replaces "on the case" with the check's detail text

### Requirement: Polite polling
The Health view SHALL poll /api/stats every ~5 seconds only while visible (pausing via the Page Visibility API), tick uptime client-side between polls, and re-render only when data changes.

#### Scenario: Hidden tab stops polling
- **WHEN** the Health view's tab is hidden
- **THEN** polling pauses until the tab is visible again

### Requirement: Titles in lists, structured description in detail
The Cases list (and any case reference in the UI, including stale-probes rows and diff headers) SHALL display the session title. The case detail view SHALL show the title as the header with the full description below, rendering `Symptom:` / `Expected:` / `Repro:` / `Context:` leading lines with bold emphasis when present.

#### Scenario: Scannable case list
- **WHEN** the Cases view lists a session titled "Login 401 after idle timeout" with a long description
- **THEN** the list row shows the title (not the description), and opening the case shows title header plus the description with its headings emphasized

#### Scenario: Legacy session display
- **WHEN** a pre-title session appears in the list
- **THEN** its derived truncated title displays and the detail view shows the original description unmodified

### Requirement: Human display names for hypotheses and probes
The UI SHALL render hypotheses and probes by derived display names ("Hypothesis 1" for `h1`, "Probe 2" for `p2`) in every view, SHALL never render a raw ID adjacent to its derived name, and SHALL defensively strip a leading self-ID prefix (e.g. "h1: ") from legacy statements at render time without modifying stored data.

#### Scenario: No duplicated labels
- **WHEN** a hypothesis stored as id `h1` with statement "h1: race in token refresh" (legacy) renders on the board
- **THEN** it displays as "Hypothesis 1 — race in token refresh" with no repeated identifier

### Requirement: Hypothesis color identity
Each hypothesis SHALL receive a deterministic color from a colorblind-safe categorical palette (assigned by index, cycling), rendered as a color chip/accent on its board card and beside every reference to it elsewhere (probes table, evidence, run views). Color SHALL always pair with the display name, never carry meaning alone. Confirmed hypotheses SHALL use the brand coral accent; killed hypotheses SHALL render muted with a visible "ruled out" status.

#### Scenario: Color follows the hypothesis
- **WHEN** Hypothesis 2 has palette color B and appears on the board and in the probes table
- **THEN** both locations show the same color chip next to "Hypothesis 2"

#### Scenario: States override palette
- **WHEN** a hypothesis is confirmed and another is killed
- **THEN** the confirmed one carries the coral accent and the killed one renders muted with its ruled-out status visible

### Requirement: Probes table naming and linkage
The probes table SHALL use column headers "Probe" and "Hypothesis", SHALL render full display names in both columns, and the hypothesis cell SHALL include that hypothesis's color chip.

#### Scenario: Probe row reads naturally
- **WHEN** probe `p3` linked to hypothesis `h2` renders in the table
- **THEN** the row reads "Probe 3" / "Hypothesis 2" with Hypothesis 2's color chip — no raw `p3`/`h2` codes

### Requirement: Product tagline in the header
The Case Board header SHALL display the product tagline "Elementary, dear developer." beside or beneath the sherlog wordmark, visually subordinate to it.

#### Scenario: Tagline present
- **WHEN** any Case Board view loads
- **THEN** the header shows the wordmark with the tagline "Elementary, dear developer."

### Requirement: Confirmed verdict panel
When a case has a confirmed hypothesis or a recorded resolution, the case detail SHALL lead with a verdict panel above the hypothesis board: the confirmed statement as the headline (coral accent), followed by labeled fact rows — Root cause, Fix, Confirmed by (probe display names and run references), and Closed (when resolved). Cases without a confirmation SHALL render no empty panel. Killed hypotheses SHALL collapse into a muted "ruled out" list beneath the active board.

#### Scenario: Solved case leads with the verdict
- **WHEN** a case with confirmed Hypothesis 2, root cause, and fix summary opens in detail view
- **THEN** the verdict panel renders first — headline statement, Root cause / Fix / Confirmed by fact rows with probe and run chips — and the ruled-out hypotheses appear muted below

#### Scenario: Open case unchanged
- **WHEN** a case with only active hypotheses opens
- **THEN** no verdict panel renders and the board appears as before

### Requirement: The case detail renders the blast radius
When a session carries a radius, the case detail (and closed-case view) SHALL render the pattern, each hit with file, line, excerpt, and a verdict badge (`sibling-bug` / `safe` / `already-covered` / `unreviewed`), plus the unreviewed count and a visible truncation notice when `truncated` is set. Rendering SHALL remain GET-only with no external origins; hits SHALL display as inert text, never as links that fetch or mutate.

#### Scenario: Radius section rendered with badges
- **WHEN** the detail view shows a case with a 4-hit radius, one hit graded `sibling-bug`
- **THEN** the pattern and all 4 hits render with their badges and the unreviewed count

#### Scenario: Truncation visible
- **WHEN** the stored radius has `truncated: true`
- **THEN** the section shows a notice that the hit cap was reached

#### Scenario: Long hit lists collapse
- **WHEN** the radius holds more hits than the preview cap
- **THEN** graded hits and the first preview rows render, and the remainder folds behind a native expander labeled with the hidden count — no script handler, content still inert text

#### Scenario: No radius, no section
- **WHEN** a case has no blast radius
- **THEN** the detail view omits the section entirely

### Requirement: Probe predictions are visible on the board
A probe carrying a prediction pair SHALL display both `expected_if_true` and `expected_if_false` in the case detail's probe listing, visually paired so the discriminating contrast reads at a glance. Prediction-less probes render unchanged.

#### Scenario: Prediction pair rendered
- **WHEN** the case detail shows a probe with predictions
- **THEN** both expected outcomes are visible and labeled (if-true / if-false)

### Requirement: Hypothesis verdicts show their evidence citation
A killed or confirmed hypothesis SHALL display its citation (probe and run, using display names) alongside the evidence note, linking the verdict to the run it came from.

#### Scenario: Confirmed suspect shows its proof
- **WHEN** a hypothesis is confirmed citing `p1` in `r2`
- **THEN** the board renders the verdict with "Probe 1 · Run 2" (display names) next to the note

### Requirement: The case header shows repro rate and pinned commit
The case header SHALL show the computed repro rate with raw counts (e.g. `reproduced 2/3`) once at least one repro-attempt run has closed, and the pinned commit SHA (shortened) when the session carries one.

#### Scenario: Header carries the determinism signal
- **WHEN** a case has closed runs with verdicts `reproduced`, `reproduced`, `not-reproduced`
- **THEN** the header shows `2/3` and the short commit when pinned

### Requirement: The resolution panel shows prevention references
The closed-case resolution view SHALL display `regression_test_ref` and the guardrail (type badge + ref text) when present, as plain text — never as executable links that mutate anything (the board remains GET-only with no external origins).

#### Scenario: Guardrail displayed inert
- **WHEN** a solved case carries `guardrail: {type: "lint", ref: "no-floating-refresh"}`
- **THEN** the resolution panel shows a "lint" badge with the ref text, with no outbound fetch or mutation

### Requirement: Fixed-check runs display their prediction
A run carrying a recorded prediction SHALL display it in the run detail and in the compare-runs view, so the observed divergence is read against the recorded claim.

#### Scenario: Prediction beside the diff
- **WHEN** the compare view diffs a reproduce run against a predicted fixed-check run
- **THEN** the prediction text is rendered above the probe divergence list
