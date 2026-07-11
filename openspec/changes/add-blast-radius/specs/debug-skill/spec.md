# debug-skill (delta)

## ADDED Requirements

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
