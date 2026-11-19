# Design: add-case-audit-and-stats

## Context

Case state is `state.json` (atomic rewrite, the final board) plus per-session `logs.jsonl` (probe events). Retention (`PruneClosedBefore`) deletes whole session directories, so neither survives as history. Gate refusals are typed sentinels from `internal/store/gates.go` and `blast_radius.go`, mapped to HTTP 4xx in one switch in `internal/daemon/server.go` (~L694) and then gone. `/api/stats` and the Health view already exist for ingest activity (`internal/daemon/stats.go`), and `internal/notes` is the precedent for a global append-only JSONL under the storage root read directly by a CLI subcommand. `add-case-next` supplies `Evaluate(sess)` (stage, `closed_from`); `add-probe-leak-guard` supplies the guard refusal path. This change lands after both.

## Goals / Non-Goals

**Goals:**

- One append-only history that survives retention and records refusals as first-class events.
- An end-to-end number with explicit definitions, recomputable at any time from that history.
- Refusals pinned to the stage they fired in, so "where does the loop break" is answerable.

**Non-Goals:**

- Any upload, sync, or team aggregation (rails replicates to MongoDB; sherlog is local-only by invariant).
- Log rotation or compaction. Records are ~200 bytes of metadata; 10k events ≈ 2 MB. Revisit only with field data; doctor can report the size.
- Claude Code session attribution. The MCP stdio server is not given the harness session ID; rails gets it from hook payloads, which sherlog's MCP path does not see. The case ID is the attribution unit.
- A "reworked" outcome (a fix that came back). sherlog has no reopen and no reliable signal linking a new case to an old fix; inventing one would make the number less honest, not more. Recall-based linking is a candidate for a later change.
- Free-text history. Statements, notes, and predictions stay in session state and are pruned with it.

## Decisions

### D-A · One global file, metadata only

`<root>/audit.jsonl`, not per-session: per-session files die with retention, which is the exact problem. Metadata-only content (IDs, codes, enums, counts, timestamps) keeps the file small, keeps pruning meaningful for privacy (the substance of a case still disappears on schedule), and makes "outlives retention" safe to promise.

### D-B · Record schema

```json
{"v":1,"ts":"…Z","case":"u8xggq67","kind":"hypothesis","stage":"judge","ver":"0.10.0",
 "hyp":"h2","status":"killed","probe":"p2","run":"r1"}
```

Common fields `v, ts, case, kind, stage, ver`, optional `backfilled:true`; kind-specific fields flat beside them. Kinds: `case_opened`, `case_closed` (`solved`, `confirmed_hyp`), `suspects_set` (`hyps`), `hypothesis`, `probe_registered` (`probe`, `hyp`, `predicted`), `probe_removed`, `run_opened` (`run`, `predicted`), `run_closed` (`run`, `verdict`, `predicted`), `radius_mapped` (`hits`, `truncated`), `radius_annotated` (`unreviewed`), `refused` (`tool`, `gate`), `guard_refused` (`layer`, `op`, `findings`, `cases`). `stage` is `Evaluate` on the session **before** the event, so a `case_closed` record's stage *is* the case's `closed_from`, and a refusal's stage is where the agent was stuck.

### D-C · Emission points

- **Accepted changes:** the store gains an injectable `Auditor` interface (`Record(Event)`), called by each mutating method after `writeState` succeeds, outside `s.mu` (same off-lock discipline as `appendLog`). Tests inject a recording fake; the default writes the file.
- **Refusals:** recorded in the store at the gate site (the method has the session and can evaluate the stage), not in the daemon's HTTP switch — the store is where the sentinel is known and it keeps the CLI and daemon paths identical. Gate codes come from a `gateCode` map from sentinel to kebab-case string in `gates.go`; a meta-test fails if any gate sentinel lacks a code, reusing the leak-guard corpus's sentinel parser.
- **Guard:** appends `guard_refused` with `O_APPEND` directly (no daemon, per the guard's contract), best-effort.

### D-D · Durability and concurrency

Appends use `O_APPEND|O_CREATE|O_WRONLY` with one `Write` per line (a line is far below `PIPE_BUF`), so the daemon and a concurrent guard process cannot interleave within a line on POSIX, and Windows' `FILE_APPEND_DATA` gives the same guarantee. No fsync per line: `state.json` is the truth and is fsynced; a crash can lose the last audit line, never corrupt state. An in-process mutex serializes daemon writers. Append failures are logged once per process (`sync.Once`) to stderr and never surface to the tool call.

### D-E · Backfill

At daemon start, if `audit.jsonl` is absent, synthesize records from every loaded session (`case_opened` at `created_at`, `suspects_set`, `probe_registered`/`probe_removed`, `run_opened`/`run_closed` at their timestamps, final `hypothesis` statuses at `updated_at`, `radius_mapped`, `case_closed` at `closed_at`) in timestamp order, each `backfilled:true`, written to a temp file and renamed into place so a crash mid-backfill leaves no partial log and backfill re-runs. Stage is computed by replaying `Evaluate` over a progressively rebuilt session copy. Refusals are not synthesized; stats disclose the backfilled case count.

### D-F · Stats are a pure function over records

`internal/stats.Compute(records []audit.Record, now time.Time, abandonAfter time.Duration) Report`, no I/O, so CLI, daemon, and tests share one implementation (rails pins its CLI and dashboard math against a shared fixture; sherlog avoids the second implementation altogether — the Case Board renders the daemon's computed `outcomes` and never re-derives). `now` matters only for abandonment; everything else depends on records alone. Medians, not means, for runs and time-to-solve — one marathon case should not define the typical one.

### D-G · Outcome definitions

`solved` requires a `fixed-check` run because a solved close without one is the agent's claim, not verified evidence — the gates allow it, so the number must distinguish it. `abandoned` exists so a case nobody closes cannot hide outside the denominator forever. Both definitions are printed with the number (rails' rule: if the definition is not next to the number, the number is a vibe).

### D-H · Surfaces

- `sherlog audit` / `sherlog stats`: stdlib `flag`, read the file directly (the `notes` precedent), `tabwriter` output; `--since` parses `Nd`/`Nh` plus Go durations.
- Daemon: `/api/stats` gains `outcomes` (computed per request; the file is small — a cache keyed on file size+mtime if profiling says so); GET `/api/sessions/{id}/audit` serves one case's records for the Timeline. Both GET-only, no CORS, consistent with the Case Board invariant.
- Case Board: `health.js` Outcomes panel; `detail.js` Timeline.

## Risks / Trade-offs

- **[Stage before an event needs `Evaluate` on every mutation]** → it is a pure pass over one small session; benchmark it in task 1.4 and confirm it is negligible next to the `state.json` fsync.
- **[Backfilled histories are approximate]** → no refusals, stages reconstructed from final state; every backfilled record is labeled and the count disclosed in stats.
- **[Lost last line on crash]** → accepted; state is authoritative and stats tolerate a missing event. Readers report skipped damaged lines rather than hiding them.
- **[Unbounded file]** → metadata-only lines keep growth trivial for a single developer; doctor/health show the size; rotation is a later change if needed.
- **[Dependency on two unmerged changes]** → tasks are ordered so `internal/audit` and `internal/stats` land and test standalone; only the stage field and guard emission wait for their prerequisites.
