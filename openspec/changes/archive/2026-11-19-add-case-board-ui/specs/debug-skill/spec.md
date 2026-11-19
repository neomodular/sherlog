# debug-skill (delta)

## ADDED Requirements

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
