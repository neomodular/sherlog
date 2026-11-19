# Proposal: fix-prerun-event-attribution

## Why

Dogfooding surfaced a real loss mode: probe events that arrive when no run is open are stored with a blank run label and are invisible to every run summary and await result. Fast scripted reproductions finish in the gap between Claude's "reproduce now" instruction and the `await_run` call opening the run — so the better the user's repro automation, the more evidence silently vanishes (observed: a fixed-check run reported zero events despite a successful reproduction).

## What Changes

- **Adoption-on-open**: when a new run opens, orphan events (blank run) whose timestamps fall after the last run boundary (previous run's close, else session start), within a ~15-minute cap, are adopted into the new run.
- Run summaries and await results disclose adopted counts (`adopted: N`) so inferred attribution is always distinguishable from direct attribution.
- Adoption survives daemon restart (append-only adoption marker; no log rewrite).
- The `/debug` skill interprets adopted counts and falls back to a fresh reproduction when adoption looks suspect.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `log-query`: ADDED — adoption-on-open behavior, boundary/cap rules, adopted-count disclosure in summaries and await results.
- `debug-skill`: ADDED — skill treats adopted evidence as valid but labeled; re-prompts for a live reproduction when adoption is ambiguous.

## Impact

- Code: `internal/store` (adoption logic, orphan flood-buffer re-keying, persistence marker, replay), `internal/daemon` (open-run path, summary/await payloads), `internal/mcp` (await_run result field), `skills/debug/SKILL.md`, tests throughout.
- Backward compatible: existing state/log files load unchanged; old orphan events beyond the cap simply remain unattributed.
- Must land before v0.2 (`diff_runs` and the Case Board would inherit the blind spot).
