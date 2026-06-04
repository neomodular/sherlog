# Design: fix-prerun-event-attribution

## Context

`store.Ingest` attributes events to the latest open run, else `Run:""` (orphans). `RunSummary`/`RunTotal` filter strictly by run ID, so orphans are stored but unqueryable per-run. The await loop's instruction→tool-call gap makes orphaning routine for fast reproductions.

## Goals / Non-Goals

**Goals:**
- Fast scripted repros that fire before `await_run` opens the run are fully attributed.
- Inferred attribution is always disclosed, never silent.
- Restart-safe without rewriting append-only logs.

**Non-Goals:**
- Attributing orphans to *closed* runs retroactively (verdicts were already given on what was visible).
- Changing the skill protocol (no new open-run tool; daemon-side fix only).

## Decisions

### D1: Adoption window = (last run boundary, now], capped at 15 minutes
On opening a **new** run (not re-attach), adopt orphans with `TS > boundary` where boundary = previous run's `ClosedAt` (else session `StartedAt`), and `TS > now-15m`. The boundary rule is what makes adoption safe: anything after the last verdict can only belong to the next attempt. The cap keeps hour-old stragglers from polluting a fresh run.

### D2: Persistence — append-only adoption marker
Adoption appends one marker line to `logs.jsonl` (e.g. `{"adopt":{"run":"r2","from":<ts>,"to":<ts>}}`); replay applies markers in order after loading events. No rewrite of existing lines, consistent with the append-only store design. In memory, adoption re-labels the events and re-keys the orphan flood buffers.

### D3: Flood-buffer re-keying
Orphan buffers live under `floodKey{run:"", probe}`. Adoption moves retained events within the window to `floodKey{run:r2, probe}` (merging if the new run already has events — it can't on open, which is why adoption happens exactly at open). If an orphan buffer spans the boundary (mixed old/new), retained events split by timestamp; the exact total for the adopted side is computed from retained in-window events plus the counter only when the buffer never truncated — when it did truncate across the boundary, the adopted total is reported as a minimum (`>= n`) with the same truncation disclosure queries already use. In practice pre-boundary orphans are rare (they exist only when a prior open didn't claim them, i.e. beyond the cap).

### D4: Disclosure shape
`ProbeSummary` gains `Adopted int`. `await_run` results and run summaries carry it through; anything adopted also keeps `Truncated` semantics unchanged. The skill guidance: adopted evidence is valid but labeled — when a run is *entirely* adopted and the verdict matters (fixed-check), prefer asking for one live reproduction if anything looks inconsistent.

## Risks / Trade-offs

- [Stale event adopted into a fresh run] → boundary + cap rules; disclosure lets Claude discount it; worst case equals today's manual re-run.
- [Marker line breaks old-binary replay] → older binaries ignore unknown JSONL lines only if coded so — they aren't; acceptable because downgrade-after-upgrade isn't a supported path (documented in README troubleshooting).
- [Split-buffer totals approximate across truncation boundary] → reported as minimum with truncation flag; exact in all common cases.

## Migration Plan

Additive field + new marker line type. Existing files load unchanged; old orphans outside any future window stay unattributed (status quo). Rollback = revert; markers in logs.jsonl are then unread (events stay orphaned, today's behavior).

## Open Questions

None.
