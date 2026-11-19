# Design: add-case-next

## Context

The session already records everything the loop's position depends on: hypothesis `status`, `created_at`/`updated_at` and evidence citations; probes with `hypothesis_id`, prediction pair, and `removed`; runs with `closed_at`, `verdict`, and an immutable fix `prediction`; the blast radius with per-hit verdicts; the resolution; and the session `closed_at`. The daemon already wraps sessions in an envelope (`withReproRate` in `internal/daemon/server.go`, `sessionState` in `internal/mcp/client.go`) for computed fields, which is the precedent for attaching another computed field. MCP tools are registered through the generic `add[In, Out]` adapter in `internal/mcp/tools.go`, each with its own output struct.

safedose-rails' `sdrails next` (`crates/rails-core/src/catalog.rs`) is the reference: deterministic derivation from recorded artifacts, human gates never inferred, anomalies reported rather than repaired, and a structural test proving the gate never reads the evaluator.

## Goals / Non-Goals

**Goals:**

- One pure function answers "where is this case, and what can happen next" for every surface.
- The human steps (reproduce, verdict, fix retest) can never be skipped by inference.
- The skill's prose state machine collapses to "follow `next`".

**Non-Goals:**

- A data-driven card catalog (`cards.json`). Rails needs one because governed repos add their own review lenses; sherlog has one fixed loop and no extension story. A Go `switch` is the honest representation; a catalog would be indirection without a second consumer.
- Choosing *which* suspect to kill or confirm. Evidence judgment stays with the agent; `next` never reads probe payloads.
- Auto-advancing anything. `next` never calls a tool or mutates state.
- New board data. If a stage cannot be derived from existing fields, the stage is wrong, not the board.

## Decisions

### D-A · Pure function in the store, beside the data it reads

`func Evaluate(sess *Session) Next` in `internal/store/next.go`, no receiver, no I/O. `Next` is `{Stage, ClosedFrom, Ready []Action, AwaitingHuman []HumanWait, Anomalies []Anomaly}` with JSON tags matching the spec (`stage`, `closed_from`, `ready`, `awaiting_human`, `anomalies`; `Action{action, target, reason, optional}`; `HumanWait{action, reason}` with actions `reproduce`, `report_verdict`, `retest_fix`; `Anomaly{code, detail}`). Lists marshal as `[]`, never `null`, so clients never special-case absence. Stage constants are a typed string, like `HypothesisStatus`.

### D-B · Precedence, first match wins

```
closed_at set                                   → closed   (closed_from = rules below)
open run with prediction                        → fix      (awaiting: retest_fix)
open run                                        → reproduce(awaiting: reproduce, report_verdict; ready: close_run)
<3 suspects | no active and no confirmed        → board    (ready: set_hypotheses)
confirmed and latest prediction run = fixed-check → cleanup (ready: remove_probe… then debug_end)
confirmed                                       → fix      (ready: [map_blast_radius?] [annotate_blast_radius?] apply_fix, await_run+prediction)
active suspect lacking unremoved predicted probe→ instrument (ready: register_probe per suspect)
latest closed non-prediction run unjudged       → judge    (ready: update_hypothesis citing run; await_run optional)
otherwise                                       → reproduce(ready: await_run)
```

Open runs come before the board checks because a wait in progress is the most urgent fact: the user may be reproducing right now.

### D-C · "Judged" is defined by recorded timestamps

A closed run counts as judged when any hypothesis has `max(created_at, updated_at)` after the run's `closed_at` — a kill, a confirm, a note-only update recording "inconclusive", or new suspects added all count. These are recorded instants, not the wall clock, so determinism holds. *Alternative considered:* judged only when a transition *cites* the run. Rejected — an inconclusive run legitimately produces no citation, and requiring one would trap the loop in `judge` forever.

### D-D · Fix state comes from predictions, not a new marker

"Fix applied" is the existence of a run carrying a prediction: `await_run(prediction=…)` is the only way a fix enters verification (harden-detective-gates D-D). The latest prediction run decides: open → `fix` awaiting the retest; closed `fixed-check` → `cleanup`; closed with any other verdict → back to `fix` with a reason naming the failed run. A confirmed suspect with no prediction run is `fix` with `apply_fix` ready — never `cleanup`, which is the human-step invariant applied to the fix.

### D-E · Anomalies are a separate pass that never changes the stage

A second function scans for the seven anomaly codes in the spec. Most are unreachable through today's gates and exist for legacy state files (pre-citation confirms) and for defense in depth: if a future gate regresses, the anomaly is the tripwire. `closed-with-live-probes` is shared with `add-probe-leak-guard`'s doctor probes check — whichever change lands second calls this pass instead of re-implementing it.

### D-F · Independence from the gates is tested structurally

A test parses `gates.go` and `blast_radius.go` (`go/parser`) and fails if either references `Evaluate` or `Next`, mirroring rails' `tests/structural.rs`. Advice must never loosen enforcement; making that a compile-time-adjacent check means it can't erode in review.

### D-G · Surfaces

- **Daemon:** the session envelope gains `Next store.Next json:"next"` beside `ReproRate`; every state-changing `/api/` handler that returns session data computes it after the mutation, under the same read that produced the response. A refused call returns the error envelope unchanged (no `next`).
- **MCP:** each affected tool's output struct gains `Next *store.Next json:"next,omitempty"`, filled from the daemon response; read-only tools (`query_logs`, `diff_runs`) are untouched. Tool descriptions mention `next` in one sentence so the model knows to read it.
- **CLI:** `sherlog next [--session ID] [--json]` opens the store directly (the `probes --stale` precedent, D10); default target is the most recently created open session. Text output is the stage track line, then `ready`, `awaiting`, `anomalies` blocks.
- **Case Board:** `detail.js` renders the track from the envelope's `next` (no client-side derivation — one evaluator), with anomalies listed under it. GET-only invariant unchanged.
- **Skill:** resume step 3 and the per-step "what now" prose are replaced by reading `next`; the checklist gains "act on `next`, judge evidence yourself".

## Risks / Trade-offs

- **[Precedence encodes a policy that may be wrong in edge cases]** → table-driven tests with one row per precedence line plus the spec scenarios; a dogfood run (task 6.2) on a real case; the skill keeps judgment, so a wrong `ready` list costs a suggestion, not an action.
- **[Agents may treat `ready` as permission]** → spec + skill say gates are authoritative; the structural test guarantees gates never consult `next`.
- **[Payload growth on every tool result]** → `next` is a few hundred bytes; acceptable against the cost of a mis-resumed case.
- **[Timestamp ties in D-C]** → comparisons are strict "after"; a hypothesis update in the same instant as a run close (sub-millisecond, practically impossible through the tool path) counts as not-judged, the conservative direction.
