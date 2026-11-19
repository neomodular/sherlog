# Proposal: add-case-audit-and-stats

## Why

sherlog cannot answer the two questions the Stage-4 workflow bar asks of any wired process: *do you know your end-to-end number?* and *can any failure be traced to the step it came from?* Today a case's history is its `state.json` — the final board, not how it got there — and retention (`PruneClosedBefore`) deletes closed sessions, so the record of every old case, solved or not, disappears with it. Gate refusals, the clearest signal of where the agent's discipline slips, are returned to the agent as an error and then lost entirely. safedose-rails meets both bars with an append-only event log that outlives its runs and a `stats` command whose outcome definitions are printed alongside the number; sherlog has the state transitions to do the same and records none of them.

## What Changes

- **Append-only case audit log** — `~/.sherlog/audit.jsonl`, one structured line per governance event: case opened/closed, suspects set, hypothesis transitions with their evidence citations, probes registered/removed, runs opened (prediction recorded or not)/closed with verdict, blast radius mapped/annotated, **every gate refusal with a stable gate code**, and every leak-guard refusal. Each line carries the case ID, the loop stage (from case-next) at which it happened, and the binary version.
  - **Metadata only** — IDs, codes, counts, verdicts, timestamps; never hypothesis statements, notes, probe payloads, or file contents. The log stays small and carries nothing a user would not want lingering after a case is pruned.
  - **Outlives retention** — pruning deletes sessions, never the audit log, so outcome history survives.
  - **Backfilled once** — on first start, existing sessions are synthesized into the log (marked `backfilled`), so the numbers are meaningful from day one; refusals cannot be backfilled and that is disclosed.
- **`sherlog audit [--session ID] [--json]`** — a case's timeline, refusals included, read from the log with no daemon.
- **`sherlog stats [--json] [--since <dur>]`** — the end-to-end number, computed from the audit log alone:
  - outcome per case — `solved` (closed solved after a passing fix check), `solved-unverified` (closed solved with no passing fix check), `unsolved`, `abandoned` (open with no activity for `abandon_after_days`), and in-flight (excluded);
  - **verified solve rate** = solved / (solved + solved-unverified + unsolved + abandoned), overall and as a weekly trend;
  - loop health — first-try fix rate, median runs and time to solve, kills before a confirm, gate refusals per case and the most-refused gates, leaks prevented by the guard, probes left behind, and the stage at which unsolved/abandoned cases stopped;
  - every definition is printed in the footer (and carried in `--json`), so the number is never mysterious.
- **Case Board** — the Health view gains an Outcomes panel (rate, trend, top refusals, where cases die); the case detail gains a Timeline of audit events with refusals pinned at the step they fired.
- **New config key** `abandon_after_days` (default 7).

## Capabilities

### New Capabilities

- `case-audit`: the audit log — event kinds, line schema, metadata-only content, durability and ordering, survival across retention, backfill, tolerant reading, and the `sherlog audit` CLI.
- `case-stats`: outcome classification, the verified solve rate and its trend, loop-health metrics, printed definitions, and the `sherlog stats` CLI and stats API surface.

### Modified Capabilities

- `case-board-ui`: ADDED — Outcomes panel on the Health view; Timeline on the case detail.
- `probe-leak-guard`: ADDED — a refusal appends an audit record, best-effort and fail-open.
- `configuration`: ADDED — the `abandon_after_days` key.

## Impact

- **Code:** new `internal/audit` (writer, reader, event types, backfill), store mutations and the gate-refusal path emit events, `internal/stats` (pure computation over events), daemon `/api/stats` gains `outcomes` and a new GET `/api/sessions/{id}/audit`, `cmd/sherlog/{audit,stats}.go`, `internal/config/keys.go`, Case Board `health.js` + `detail.js`, the guard's refusal path.
- **Docs (same PR):** new `docs/audit-and-stats.md` (event schema, outcome definitions), `docs/configuration.md` (new key), README commands, `docs/architecture.md` (the audit as history vs `state.json` as truth), `CLAUDE.md` binary commands.
- **Depends on:** `add-case-next` (stage labels, `closed_from`) and `add-probe-leak-guard` (guard refusals) — this change lands after both.
- **Security invariants:** unchanged — local file, never uploaded, no telemetry; content is metadata only.
- **Compatibility:** additive files and fields; no state-file change; no tool contract change.
