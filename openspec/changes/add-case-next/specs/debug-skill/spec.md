# debug-skill (delta)

## ADDED Requirements

### Requirement: The skill drives the loop from `next`
After every state-changing tool call and on resume, the skill SHALL take the loop's position from the result's `next` object — perform a `ready` action, or ask the user for what is in `awaiting_human` — rather than re-deriving the stage from the board or conversation memory. The skill SHALL keep the evidence judgment itself: `next` names the stage and candidate actions, never which suspect the evidence kills or confirms.

#### Scenario: Resume picks up at the evaluated stage
- **WHEN** `debug_resume` returns `next.stage: fix` with `apply_fix` ready and `map_blast_radius` optional
- **THEN** the skill restates the case from the board and proceeds to the fix, offering the radius as optional

#### Scenario: Judgment stays with the skill
- **WHEN** `next.stage` is `judge` citing run `r2`
- **THEN** the skill reads `r2`'s evidence and decides which suspects to kill or confirm on its own

### Requirement: Anomalies are surfaced, not repaired silently
When `next.anomalies` is non-empty, the skill SHALL tell the user what each anomaly means and SHALL NOT edit the board to make it disappear without the user's agreement.

#### Scenario: Leftover probes after a close
- **WHEN** a resumed closed case reports `closed-with-live-probes`
- **THEN** the skill lists the probes and offers to remove them, without acting before the user agrees

### Requirement: `next` is advice; gates remain authoritative
When a gate refuses an action that `next` listed as ready, the skill SHALL follow the gate's repair instruction (per the existing gate-repair rule) and SHALL NOT treat the `next` listing as permission.

#### Scenario: Ready action refused
- **WHEN** `next` lists `update_hypothesis` and the confirm is refused for a missing prediction on the cited probe
- **THEN** the skill performs the named repair and does not cite `next` as grounds to retry unchanged
