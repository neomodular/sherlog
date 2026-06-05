# MCP Tools Reference

Every MCP tool the `sherlog mcp` server registers, with its parameters, result
shape, and an example. These tools are how the `/debug` skill drives an
investigation; you do not call them by hand, but this is the contract they
implement.

Conventions used below:

- **`session_id`** is the token from `debug_start`. Tools that take it require it
  unless noted; a few (`debug_resume`, `debug_end`, `diff_runs`) accept an omitted
  `session_id` and fall back to the **latest open session**.
- Run IDs are `r1`, `r2`, … (assigned by `await_run`). Probe IDs are whatever you
  register (convention `p1`, `p2`, …). Hypothesis IDs are `h1`, `h2`, … (assigned
  by `set_hypotheses`).
- Every tool ensures the daemon is running before it acts, auto-spawning it if
  needed. A foreign process on the port surfaces an actionable error (see
  [troubleshooting.md](troubleshooting.md)).

## Session lifecycle

### `debug_start`

Open a new investigation. Start every session here.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `title` | string | no | Short, specific case title (≤60 chars), e.g. `Login 401 after idle timeout`. The case identity shown in lists, the banner, recall, and resume. Omit and the daemon derives a word-boundary-truncated fallback from the description (backward compatible). |
| `bug_description` | string | yes | The bug being investigated. The skill writes it as soft-structured plain text (`Symptom:` / `Expected:` / `Repro:` / `Context:` lines, only those with real content). |

**Result:**

```json
{
  "session_id": "a1b2c3",
  "title": "Login 401 after idle timeout",
  "probe_contract": {
    "url_template": "http://127.0.0.1:2218/log/a1b2c3/<probe>",
    "note": "Fire-and-forget: never await the call, never set a JSON Content-Type …",
    "one_liners": { "js": "...", "python": "...", "go": "...", "ruby": "...", "curl": "..." }
  },
  "preferences": { "verbosity": "detective", "color": "auto" },
  "warn_same_cwd": null,
  "related_cases": []
}
```

- `title` — the case identity, echoed back: the supplied title, or the daemon's
  derived fallback when omitted. Always non-empty.
- `probe_contract` — the URL template and per-language one-liners (see
  [probe-contract.md](probe-contract.md)).
- `preferences` — skill presentation (`verbosity`, `color`) from effective config.
- `warn_same_cwd` — a concurrent open session in the same directory, or `null`.
  Advisory; does not block.
- `related_cases` — possibly-related **solved** past cases recall surfaced for
  this description (`session_id`, `title`, `description`, `root_cause`,
  `fix_summary`, `score`). Each is identified by its `title`. Leads only — never
  evidence.

### `debug_resume`

Reconstruct an investigation after context loss (returns the full session state).

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | no | The session to resume; omit for the latest open one. |

**Result:** the full `Session` object — `id`, `title`, `description`, `cwd`,
`created_at`, `closed_at`, `hypotheses[]`, `probes[]`, `runs[]`, and `resolution`
(when solved). `title` is always non-empty (a derived fallback for legacy sessions).

### `debug_end`

Close the investigation and get the cleanup checklist.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | no | The session to close; omit for the latest open one. |
| `root_cause` | string | no | The confirmed root cause, when solved. |
| `fix_summary` | string | no | A concise summary of the fix, when solved. |
| `confirmed_hypothesis_id` | string | no | The hypothesis confirmed as culprit, e.g. `h2`. |

Supply all three resolution fields only when a root cause was confirmed; omit
them all to close unsolved (recorded as such, excluded from recall). A solved
close becomes recall material for future investigations.

**Result:**

```json
{
  "unremoved_probes": [ { "id": "p2", "file": "auth.go", "line": 88, "hypothesis_id": "h1", "removed": false, "created_at": "..." } ],
  "greppable_fragment": "http://127.0.0.1:2218/log/a1b2c3/",
  "cleanup_complete": false
}
```

`cleanup_complete` is `true` only when no registered probe remains unremoved. Grep
the repo for `greppable_fragment` and require zero matches before declaring the
case closed.

## Hypothesis board

### `set_hypotheses`

Replace the board with a list of suspect statements (provide at least three).

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `hypotheses` | string[] | yes | Suspect statements; at least three. |

**Result:** `{ "board": [ Hypothesis, … ] }`. Each `Hypothesis` is
`{ id, statement, status, note, created_at, updated_at }` with `id` assigned
(`h1`, `h2`, …) and `status` set to `active`.

### `update_hypothesis`

Update a hypothesis's status and attach an evidence note.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `id` | string | yes | The hypothesis ID, e.g. `h2`. |
| `status` | string | yes | One of `active`, `killed`, `confirmed`. |
| `note` | string | no | Evidence note explaining the status. |

An invalid `status` is rejected client-side with a clear message before reaching
the daemon.

**Result:** the updated `Hypothesis`.

## Probe registry

### `register_probe`

Record a placed probe so cleanup is guaranteed findable.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `id` | string | yes | The probe ID used in its URL, e.g. `p3`. |
| `file` | string | yes | Source file the probe line sits in. |
| `line` | int | yes | Line number of the probe. |
| `hypothesis_id` | string | yes | The hypothesis this probe discriminates. |
| `note` | string | no | Optional note. |

**Result:** the saved `Probe`:
`{ id, file, line, hypothesis_id, note, removed, created_at }` (`removed` is
`false` until you mark it removed).

### `remove_probe`

Mark a probe removed after its line has been deleted from the code.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `id` | string | yes | The probe ID to mark removed. |

**Result:** `{ "removed": true, "probe_id": "p3", "session_id": "a1b2c3" }`.

## Runs and queries

### `await_run`

Open (or re-attach to) a run and block until probe activity goes quiet or the
timeout elapses. Re-invoke after a timeout to keep waiting on the same run for a
long reproduction.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `timeout_s` | int | no | Max seconds to wait; defaults to 120, clamped to the configured `await_max_timeout_seconds`. |

The call blocks. Once activity begins, it returns early after the configured
`await_debounce_seconds` of quiet; with no activity at all it returns at timeout
reporting zero events.

**Result:**

```json
{
  "run": { "id": "r1", "started_at": "...", "closed_at": null },
  "summary": [
    { "probe": "p1", "run": "r1", "total": 2, "adopted": 0, "truncated": false, "events": [ … ] },
    { "probe": "p2", "run": "r1", "total": 0, "adopted": 0, "truncated": false, "events": null }
  ],
  "reason": "quiet",
  "total_seen": 2
}
```

- `summary` lists **every registered probe**, including ones that fired zero times
  (`total: 0`) — that zero is the signal used to kill a hypothesis.
- `adopted` is how many events were attributed by pre-run adoption rather than
  live ingest during this run (see [architecture.md](architecture.md)).
- `truncated` discloses flood control dropped a middle (`events` then holds the
  first-N and last-N only).
- `reason` is `"quiet"`, `"timeout"`, or `"deadline"`.
- `total_seen` is events observed during *this* wait (excludes the adopted
  baseline).

### `close_run`

Record the user's verdict on the latest open run.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `verdict` | string | yes | One of `reproduced`, `not-reproduced`, `fixed-check`. |

An invalid verdict is rejected client-side. Closing when no run is open is a
conflict, not a crash.

**Result:** the closed `Run` (`{ id, started_at, closed_at, verdict }`).

### `query_logs`

Query collected evidence by probe and/or run, with truncation disclosed. Never
dumps raw logs.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `probe` | string | no | Limit to one probe ID. |
| `run` | string | no | Limit to one run ID. |
| `limit` | int | no | Cap events returned per bucket. |

**Result:** `{ "results": [ QueryResult, … ] }`, each
`{ probe, run, total, adopted, truncated, events[] }`. A `probe` filter that
matched no bucket returns an explicit `total: 0` record (so "fired zero times" is
distinguishable from "no data").

### `diff_runs`

Compare two runs of one investigation probe-by-probe. Divergent probes (fired in
only one run, or a count ratio ≥10×) are listed first. Use it to confirm a root
cause — e.g. diff a reproduce run against a fixed-check run.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `run_a` | string | yes | First run ID to compare, e.g. `r1`. |
| `run_b` | string | yes | Second run ID, e.g. `r3`. |
| `session_id` | string | no | The investigation; omit for the latest open one. |

Naming the same run twice, or a run that does not exist in the session, is
rejected as a client error.

**Result:**

```json
{
  "session": "a1b2c3",
  "run_a": "r1",
  "run_b": "r3",
  "probes": [
    {
      "probe": "p1",
      "a": { "run": "r1", "fired": true, "total": 14, "adopted": 0, "truncated": false, "first": { … }, "last": { … } },
      "b": { "run": "r3", "fired": false, "total": 0, "adopted": 0, "truncated": false },
      "divergent": true
    }
  ]
}
```

Each `ProbeDiff` carries both runs' sides (`a`, `b`) with fired/total/sample
disclosure, and a `divergent` flag; divergent probes sort to the top.

## Field notes

### `report_observation`

File a private field note when **sherlog itself** behaves unexpectedly (zero
events despite a confirmed reproduction, await/debounce oddities, cleanup-gate
surprises, tool errors). This is *not* for difficulties with the user's own bug —
only sherlog's behavior.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `note` | string | yes | The observation about sherlog's own behavior. |
| `category` | string | yes | One of `tool-bug`, `friction`, `anomaly`, `other`. |
| `session_id` | string | no | The active investigation, when one is open. |

Fire-and-forget: it never blocks an investigation and is never shown to the user.
Any failure — including the daemon being unreachable — is swallowed.

**Result:** `{ "filed": true }` (`false` on a swallowed failure; never a tool
error). Notes are appended to `~/.sherlog/field-notes.jsonl` and read with
`sherlog notes` — see [configuration.md](configuration.md) and
[troubleshooting.md](troubleshooting.md).
