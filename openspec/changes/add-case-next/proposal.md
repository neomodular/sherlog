# Proposal: add-case-next

## Why

The store gates say what the detective loop *forbids*; nothing says what comes *next*. That derivation lives in prose (`skills/debug/SKILL.md` §resume: "still gathering evidence → another `await_run`; one suspect confirmed but the fix not yet applied → map the blast radius, then fix; fix applied → fixed-check; everything verified → cleanup gate"), so a resumed session — after `/clear`, compaction, or days away — has to re-interpret the board on its own, which is exactly the conversation-memory reasoning sherlog's founding rule distrusts. safedose-rails solved the same problem with `sdrails next`: a deterministic evaluator that derives *ready / awaiting a person / anomalies* from recorded state alone, never infers a human step, and is read by every surface. sherlog has the board data to do the same; it lacks the function.

## What Changes

- **A deterministic next-step evaluator** over a session's recorded state: returns the current `stage` of the loop (`board` → `instrument` → `reproduce` → `judge` → `fix` → `cleanup` → `closed`), the `ready` actions (tool name + target + one-line reason), what is `awaiting_human` (reproducing the bug, reporting the verdict), and `anomalies` (recorded state the loop could not have produced legitimately). Pure function of the session — no model, no clock-dependent heuristics, same board ⇒ same answer.
- **Human steps are never inferred.** A run is reproduced only when its verdict says so; a fix is in verification only when a run carries a recorded prediction. Nothing behind an unmet human step is reported ready.
- **Evaluator and gates stay independent.** Gates keep deciding what is refused; the evaluator only advises. A structural test forbids the gate code from calling the evaluator, so advice can never loosen enforcement.
- **Every surface reads the same answer:**
  - `debug_resume` and every state-changing MCP tool response carry an additive `next` object, so the skill learns the next step from the tool result rather than re-deriving it.
  - `sherlog next [--session ID] [--json]` reads the store directly (no daemon), like `probes --stale`.
  - The Case Board's case detail shows a stage track (`board ✓ instrument ✓ reproduce ● judge ○ fix ○ cleanup ○`) and lists anomalies.
- **Skill simplified:** the resume section's prose state machine is replaced by "read `next`, do the ready action, ask the human for what is awaiting them". Judgment (which suspect the evidence kills) stays with the skill.

No new board data: every stage is derivable from fields already recorded (hypothesis statuses and citations, probe predictions and removal, run verdicts and predictions, blast radius, resolution, `closed_at`).

## Capabilities

### New Capabilities

- `case-next`: the next-step evaluator — stages, derivation rules, human-step invariant, anomaly detection, independence from the gates — and its CLI surface `sherlog next`.

### Modified Capabilities

- `mcp-server`: ADDED — `next` carried on `debug_resume` and state-changing tool results.
- `case-board-ui`: ADDED — stage track and anomaly list on the case detail view.
- `debug-skill`: ADDED — the skill drives the loop from `next` instead of re-deriving the stage, while keeping evidence judgment.

## Impact

- **Code:** new `internal/store/next.go` (+ tests, structural test), daemon session envelope and state-changing `/api/` responses gain `next`, MCP output structs gain `next`, `cmd/sherlog/next.go`, `internal/daemon/ui/assets/detail.js` + `board.css`, `skills/debug/SKILL.md`.
- **Docs (same PR):** `docs/tools-reference.md` (the `next` object on each affected tool), `docs/architecture.md` (evaluator vs gates), README command list, `CLAUDE.md` binary-commands block.
- **Compatibility:** additive response field only; no state-file change, no tool input change, no behavior change in any gate.
- **Dependencies:** none.
- **Related:** `add-probe-leak-guard`'s `doctor` check for probes left by closed cases is the same condition as this change's `closed-with-live-probes` anomaly — whichever change lands second reuses the other's code instead of duplicating it.
