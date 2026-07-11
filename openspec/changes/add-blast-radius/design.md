# Design: add-blast-radius

## Context

`harden-detective-gates` hardens the loop through the confirm: predictions, evidence citations, state-machine gates. What happens *after* a confirmed root cause is still narrative — an agent saying "I grepped for other occurrences" is exactly the class of claim (unrunnable, unrecorded, unverifiable) the rest of sherlog refuses to accept. The daemon already holds the two facts a trustworthy sibling search needs: the session `cwd` and the confirmed culprit's validated probe location.

Same threat model as `harden-detective-gates`: gates target drift, sloppiness, and hallucination — not an adversarial agent.

## Goals / Non-Goals

**Goals:**

- Sibling hits are daemon-recorded facts: the agent supplies a pattern and judgments, never the hit list.
- A pattern that does not match the known culprit is rejected before it can manufacture false coverage.
- Annotations can only reference hits the daemon found; unreviewed hits stay visible.
- Bounded, read-only, local-only scanning — no new invariant risk.

**Non-Goals:**

- Fixing sibling sites, remediation planning, or gating `debug_end` on the radius (it is optional evidence, not a close requirement).
- Cross-repo or multi-root search; non-regex query engines; semantic/AST matching.
- Retaining search history (latest radius wins).

## Decisions

### D-A · The daemon executes the search; the agent only proposes the pattern

`map_blast_radius(session_id, pattern, note?)` compiles the pattern with Go's stdlib `regexp` and walks the session `cwd` with `filepath.WalkDir`. Hits are `{file, line, excerpt}` (excerpt trimmed to ~200 chars). The MCP tool is a pass-through; the search runs in the daemon because it owns the session state the gates need (D6).

*Alternative considered:* verify agent-claimed hits by re-running the agent's query and comparing sets (the original framework's formulation). Rejected — daemon execution is strictly stronger and simpler: there is no claimed set to compare, so under-reporting and invention are impossible by construction, not detected after the fact.

*Alternative considered:* exec `rg`. Rejected — not guaranteed installed; stdlib-only is a project rule, and `regexp` + `WalkDir` cover the need.

### D-B · Bounded scan, disclosed truncation

Fixed bounds in this change (constants, not config keys — YAGNI until dogfooding says otherwise): skip `.git` directories entirely; skip files whose first 8 KB contain a NUL byte (binary sniff); skip files over 2 MB; never follow symlinks; stop at 500 hits and set a `truncated` flag that every surface displays. This mirrors flood control's philosophy: bound storage, always disclose the bound.

### D-C · False-coverage gate, sequenced before the fix

The tool is rejected unless the board has a hypothesis with status `confirmed`, and rejected when the culprit file — the `file` of the probe cited in that hypothesis's confirm citation (from `harden-detective-gates`) — is absent from the hit set. The error names the culprit file and states the pattern does not even match the confirmed bug.

The skill runs the search **after confirm and before applying the fix**, while the anti-pattern still exists at the culprit site. There is deliberately **no override flag**: an escape hatch for "the culprit was already fixed" is exactly the bypass a drifting agent would reach for. A case resumed post-fix simply proceeds without a radius (it is optional).

*Alternative considered:* `culprit_already_fixed` override. Rejected — see above; the sequencing rule is cheap and the gate stays airtight.

### D-D · Annotations are set-checked against recorded hits

`annotate_blast_radius(session_id, annotations[])` takes `{file, line, verdict, note?}` entries with `verdict ∈ {sibling-bug, safe, already-covered}` (enum validated). The daemon rejects any annotation whose `{file, line}` is not in the recorded hit set — the agent cannot grade sites the search did not find. Partial annotation is legal; hits without a verdict remain `unreviewed` and every rendering surface shows the unreviewed count. Multiple calls merge by `{file, line}` (later verdicts overwrite).

### D-E · One radius per session, replace semantics

The session stores a single `BlastRadius {pattern, note, searched_at, truncated, hits[]}`. A re-run (refined pattern) replaces the whole radius, clearing annotations — they graded hits of a different search and must not silently carry over. Persisted as an additive `state.json` field; legacy sessions load with it absent.

*Alternative considered:* retaining search history like runs. Rejected — no consumer for stale radii; latest-wins keeps the model and the board simple.

### D-F · Recall gains the pattern text

The radius pattern joins the recall corpus for solved cases (alongside description, root cause, hypothesis statements), so a future investigation can surface "we hunted this exact anti-pattern before." Annotated hit files are *not* indexed — file paths are noise across projects.

### D-G · Read surface and invariants

This is the first place the daemon reads user file *contents* (probe location checks only stat). Bounds: read-only, rooted at the session `cwd`, never leaves the machine, results stored under `~/.sherlog/` like everything else. Go's RE2 engine guarantees linear-time matching, so a pathological pattern cannot wedge the daemon; the walk runs outside the store lock so ingest and awaits are never blocked by a scan.

## Risks / Trade-offs

- [Large repos make the walk slow] → size/binary/`.git` skips do most of the pruning; the 500-hit cap short-circuits pathological patterns; the scan holds no store lock. If dogfooding shows pain, bounds become config keys (deferred, D-B).
- [Culprit file legitimately unmatched because the agent fixed first, then searched] → deliberate: the sequencing rule (search before fix) is in the skill, the gate error explains it, and the radius is optional. No override (D-C).
- [Regex is a blunt instrument for "same defect elsewhere" (misses renamed variants, matches comments)] → accepted: the agent grades every hit (`safe` is a legitimate verdict), and a recorded imperfect search still beats an unrecorded perfect claim. Semantic matching is a non-goal.
- [Excerpts of user code land in `state.json`] → same trust class as probe payloads already persisted there; local-only, never uploaded.
- [Hit cap truncation could hide real siblings] → truncation is always disclosed on every surface, mirroring flood control; the agent is instructed to narrow the pattern and re-run.

## Migration Plan

1. Implement after `harden-detective-gates` lands — the false-coverage gate reads the confirm citation that change introduces.
2. Additive `state.json` field; old binaries ignore it (unknown-field-tolerant decoding), old state loads with no radius. Rollback = previous release.
3. New tools only; no existing tool schema changes, so skill/binary version skew degrades to "tool not found," not wrong behavior. Docs ship in the same PR per convention.
4. Dev note: after `go install`, kill the resident daemon so the new binary respawns.

## Open Questions

(none — all decisions above are settled for this change)
