# case-board-ui (delta)

## ADDED Requirements

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
