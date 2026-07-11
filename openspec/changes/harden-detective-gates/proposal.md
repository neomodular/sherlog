# Proposal: harden-detective-gates

## Why

The detective loop's rigor lives almost entirely in `skills/debug/SKILL.md` prose: the store accepts a board with a single hypothesis, `update_hypothesis` accepts `confirmed` with no evidence at all, `debug_end` records a `confirmed_hypothesis_id` it never checks against the board, and `register_probe` records any file/line without looking. The daemon makes probe *evidence* unfakeable (live ingest, zero-fill, adoption and truncation disclosure) but leaves the agent's *interpretation* of that evidence on the honor system — exactly the layer a drifting or pressured agent can rationalize around. Sharpest instance: the fixed-check step says "predict, out loud, how the evidence should change" — the prediction lives in conversation memory, the one place sherlog's founding rule says never to trust.

This change moves the loop's gates from prose into the daemon/store as schema checks (omission/malformation), executable checks (reality-mismatch), and cross-checks against the store's own data (fabrication).

## What Changes

- **Structured probe predictions** — `register_probe` accepts `expected_if_true` / `expected_if_false` (both-or-neither; rejected when equal). "Discriminating" becomes schema instead of an adjective in a note.
- **Evidence-linked verdicts** — killing or confirming a hypothesis requires citing `probe_id` + `run_id`; the daemon cross-checks the citation against its own store (probe registered, run exists and is closed) before accepting the transition. Free-text notes remain, but they no longer carry the burden alone.
- **State-machine gates in the store** (stricter validation on existing tools):
  - `set_hypotheses` rejects a board of fewer than 3 statements.
  - `confirmed` requires ≥1 closed run with verdict `reproduced`, and the confirming citation must point at a probe that carries predictions.
  - A solved `debug_end` requires `confirmed_hypothesis_id` to match a hypothesis whose status is `confirmed` on the board.
  - `close_run(verdict=fixed-check)` requires a fix prediction recorded on the run *before* its evidence was returned.
- **Recorded fix prediction** — `await_run` accepts an optional `prediction` (how the evidence should change if the fix is right), stamped on the run at open time and shown alongside `diff_runs` output. Prerequisite for a fixed-check verdict.
- **Repro rate + environment pinning** — a computed repro rate (`reproduced` / closed repro-attempt runs) surfaced in run payloads and the Case Board; `debug_start` records the repository's commit SHA alongside `cwd` when available.
- **Probe location check** — `register_probe` verifies the file exists and the line is within it (resolved against the session `cwd`); a miss is an actionable error, not a stored fiction.
- **Resolution references** — solved `debug_end` accepts optional `regression_test_ref` and `guardrail` (`{type: test|lint|alert|doc, ref}`); recorded and displayed, never executed.
- **Skill updated** to author the structured fields and lean on daemon errors as its discipline backstop instead of prose alone.

Out of scope (per the project invariants — one stdlib binary, local-only, no telemetry): ticket-tracker dedupe, sandbox orchestration, git-bisect automation, CI/test-suite/coverage gates, PR authoring. Those belong to the agent layer around sherlog; sherlog records references at most.

## Capabilities

### New Capabilities

(none — every gate hardens an existing capability)

### Modified Capabilities

- `session-state`: ADDED — probe prediction fields; evidence citations on hypothesis transitions; store-enforced state-machine gates (min board size, confirm prerequisites, solved-close validation, fixed-check prediction); run predictions; computed repro rate; session commit SHA; resolution reference fields. All disk fields additive; legacy state loads unchanged.
- `mcp-server`: ADDED — schema changes for `register_probe`, `update_hypothesis`, `await_run`, `debug_start`, `debug_end`; probe file/line existence check; actionable validation errors for every rejected transition.
- `debug-skill`: ADDED — authoring rules for predictions and evidence citations; fixed-check prediction obligation; recovery guidance when the daemon rejects a transition.
- `case-board-ui`: ADDED — display probe predictions, hypothesis evidence citations, repro rate, pinned commit, and resolution references.

## Impact

- Code: `internal/store` (types + transition validation), `internal/daemon` (API payloads), `internal/mcp` (tool schemas + probe location check), `internal/daemon/ui/` (board rendering), `skills/debug/SKILL.md`, `docs/tools-reference.md` and `docs/architecture.md` (docs-update convention).
- Backward compatible **on disk**: every new field is additive; pre-change `state.json` files load and resume.
- Behavior-stricter **on the tool contract**: calls the store previously accepted leniently (a 1-hypothesis board, an evidence-free confirm, an unvalidated solved close) are now rejected with actionable errors. The shipped skill is the only caller and is updated in the same change.
