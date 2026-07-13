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
  "commit": "9f3a1c2e5b7d…",
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
- `commit` — the HEAD commit SHA the daemon pinned on the session when the `cwd`
  is a git work tree (a fixed-argv `git -C <cwd> rev-parse HEAD` with a short
  timeout), **omitted** when the cwd is not a repo, git is absent, or resolution
  fails. Recording only — no gate consumes it; it anchors the case to a known tree
  state. Never blocks a session.
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
`commit` (the pinned HEAD SHA, omitted for a non-git tree), `created_at`,
`closed_at`, `hypotheses[]` (each with its `evidence_probe_id`/`evidence_run_id`
citation once killed or confirmed), `probes[]` (each with any `expected_if_true`/
`expected_if_false` pair), `runs[]` (each with any fix `prediction`), and
`resolution` (when solved) — plus a computed **`repro_rate`** alongside the
session:

```json
{
  "id": "a1b2c3",
  "title": "Login 401 after idle timeout",
  "commit": "9f3a1c2e5b7d…",
  "hypotheses": [ … ], "probes": [ … ], "runs": [ … ],
  "repro_rate": { "reproduced": 2, "not_reproduced": 1, "rate": 0.667 }
}
```

`title` is always non-empty (a derived fallback for legacy sessions). `repro_rate`
is derived at read time — never stored — from the session's closed runs:
`reproduced / (reproduced + not-reproduced)`, with fixed-check runs excluded from
the denominator (see [architecture.md](architecture.md), "Mechanical gates"). An
empty denominator reports `{0, 0, 0}`.

### `debug_end`

Close the investigation and get the cleanup checklist.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | no | The session to close; omit for the latest open one. |
| `root_cause` | string | no | The confirmed root cause, when solved. |
| `fix_summary` | string | no | A concise summary of the fix, when solved. |
| `confirmed_hypothesis_id` | string | no | The hypothesis confirmed as culprit, e.g. `h2`. Must name a hypothesis whose status is `confirmed` on the board. |
| `regression_test_ref` | string | no | Name/ref of a regression test that now covers the bug, when one exists. |
| `guardrail` | object | no | A prevention control `{ "type": test\|lint\|alert\|doc, "ref": "…" }`; `ref` is free text. |

Supply **all three** of `root_cause` / `fix_summary` / `confirmed_hypothesis_id`
only when a root cause was confirmed; omit them all to close unsolved (recorded as
such, excluded from recall). A solved close becomes recall material for future
investigations.

The daemon **validates a solved close** (see [architecture.md](architecture.md),
"Mechanical gates"): supplying *any* resolution field — including a lone
`regression_test_ref` or `guardrail` — requires all three core fields, and
`confirmed_hypothesis_id` must reference a hypothesis that is `confirmed` on the
board. A `guardrail.type` outside the enum is rejected, and every resolution text
field (`root_cause`, `fix_summary`, `regression_test_ref`, the guardrail `ref`)
must be **single-line plain text** — control characters and newlines are rejected,
since these fields feed recall and the Case Board. On any of these, the tool
returns the daemon's one-line repair instruction and **the session stays open** —
it is never silently downgraded to unsolved. Prevention references are recorded
and displayed only; sherlog never fetches or runs them (local-only invariant).

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

Replace semantics: the list becomes the whole board, so a mid-investigation split
still resubmits the survivors plus the new suspect(s). The daemon **rejects a
board of fewer than three statements** with an actionable error and leaves the
existing board unchanged (see [architecture.md](architecture.md), "Mechanical
gates").

**Result:** `{ "board": [ Hypothesis, … ] }`. Each `Hypothesis` is
`{ id, statement, status, note, evidence_probe_id, evidence_run_id, created_at,
updated_at }` with `id` assigned (`h1`, `h2`, …) and `status` set to `active`. The
two `evidence_*` fields are empty until the suspect is killed or confirmed with a
citation.

### `update_hypothesis`

Update a hypothesis's status and attach an evidence note.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `id` | string | yes | The hypothesis ID, e.g. `h2`. |
| `status` | string | yes | One of `active`, `killed`, `confirmed`. |
| `note` | string | no | Evidence note explaining the status. |
| `probe_id` | string | when `killed`/`confirmed` | The probe whose evidence justifies the verdict, e.g. `p3`. |
| `run_id` | string | when `killed`/`confirmed` | The closed run whose evidence justifies the verdict, e.g. `r2`. |

An invalid `status` is rejected client-side with a clear message before reaching
the daemon; the store enforces the same enum, so a raw API caller cannot write an
out-of-enum status to the board either. A `killed` or `confirmed` transition **must cite `probe_id` and
`run_id`** — this too is checked client-side (a `refine` to `active` needs
neither). The daemon then cross-checks the citation against its own registry and
applies the confirm gate (see [architecture.md](architecture.md), "Mechanical
gates"): the probe must be registered, the run must exist **and be closed with a
verdict**, and a `confirmed` additionally requires the session to have ≥1 run
closed `reproduced` and the cited probe to carry a prediction pair. A citation to
a probe that fired **zero times** is valid — "fired zero times" is evidence. Each
daemon rejection surfaces verbatim with a one-line repair instruction. The
accepted citation is persisted on the hypothesis as `evidence_probe_id` /
`evidence_run_id`.

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
| `expected_if_true` | string | no (paired) | What the payload shows if the hypothesis is true. |
| `expected_if_false` | string | no (paired) | What the payload shows if the hypothesis is false. |
| `note` | string | no | Optional note. |

The prediction pair is **both-or-neither**, and when present the two must
**differ** (trimmed, case-insensitive) — a pair that reads the same under either
outcome is rejected. A plain path tracer may omit both; but only a probe carrying
the pair can later be cited to *confirm* a hypothesis (see the confirm gate under
`update_hypothesis`).

The daemon also **verifies the location** (see [architecture.md](architecture.md),
"Mechanical gates"): it resolves `file` against the session `cwd` (absolute paths
used as-is), and rejects a file that does not exist or a `line` past the file's
last line, with an error naming the resolved absolute path. No probe is registered
on a miss.

**Result:** the saved `Probe`:
`{ id, file, line, hypothesis_id, expected_if_true, expected_if_false, note,
removed, created_at }` (`removed` is `false` until you mark it removed; the two
`expected_*` fields are echoed back, omitted when not supplied).

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
| `prediction` | string | no | Fix prediction — how the evidence should change if the candidate fix is right. Required before a `fixed-check` close. |

The call blocks. Once activity begins, it returns early after the configured
`await_debounce_seconds` of quiet; with no activity at all it returns at timeout
reporting zero events.

`prediction` is the recorded fix-check claim. The daemon stamps it on the run **at
call receipt — before this call returns any summary** — with a `prediction_at`
timestamp, and it is **immutable once set** (supplying it on a re-attach whose run
has none is accepted; a run that already carries one keeps it). It is the
prerequisite for a `fixed-check` verdict: `close_run(fixed-check)` is rejected when
the open run carries no prediction (see [architecture.md](architecture.md),
"Mechanical gates"). Supply it only for the fixed-check reproduction, not for
initial repro runs.

**Result:**

```json
{
  "run": { "id": "r1", "started_at": "...", "closed_at": null, "prediction": "…", "prediction_at": "…" },
  "summary": [
    { "probe": "p1", "run": "r1", "total": 2, "adopted": 0, "truncated": false, "events": [ … ] },
    { "probe": "p2", "run": "r1", "total": 0, "adopted": 0, "truncated": false, "events": null }
  ],
  "reason": "quiet",
  "total_seen": 2,
  "repro_rate": { "reproduced": 2, "not_reproduced": 1, "rate": 0.667 }
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
- `repro_rate` is the session's computed determinism signal — `reproduced /
  (reproduced + not-reproduced)` over closed runs (fixed-check excluded), with the
  raw counts. Report determinism from this fraction; never assert it from memory.
- `run.prediction` / `run.prediction_at` echo the recorded fix prediction when the
  run carries one.

### `close_run`

Record the user's verdict on the latest open run.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `verdict` | string | yes | One of `reproduced`, `not-reproduced`, `fixed-check`. |

An invalid verdict is rejected client-side. Closing when no run is open is a
conflict, not a crash. A **`fixed-check`** verdict is rejected when the open run
carries no recorded fix `prediction` (see [architecture.md](architecture.md),
"Mechanical gates"); the error instructs re-awaiting with `await_run(prediction=…)`
and reproducing once more.

**Result:** the closed `Run` (`{ id, started_at, closed_at, verdict, prediction,
prediction_at }`; the two `prediction*` fields are present when the run carried a
fix prediction).

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
  ],
  "prediction_b": "p1 token now populated; p5 fires zero times"
}
```

Each `ProbeDiff` carries both runs' sides (`a`, `b`) with fired/total/sample
disclosure, and a `divergent` flag; divergent probes sort to the top. When either
compared run carries a recorded fix `prediction`, it is included as `prediction_a`
/ `prediction_b` (omitted when the run has none) so the divergence is judged
against the recorded claim rather than conversation memory — typically the
fixed-check run's prediction shows up as `prediction_b`.

## Blast radius

A confirmed root cause rarely stands alone — the same anti-pattern usually recurs
at sibling call sites. These two tools record a **daemon-executed** sibling search
and the agent's per-hit judgment, so "I checked for other occurrences" becomes a
recorded fact rather than an unrunnable claim. Neither tool gates `debug_end` — the
radius is optional evidence. See [architecture.md](architecture.md), "Mechanical
gates", for the read surface and the false-coverage gate.

### `map_blast_radius`

Run a regex sibling search under the session `cwd`. Call it **after a hypothesis is
confirmed and before applying the fix**, while the anti-pattern still exists at the
culprit site.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `pattern` | string | yes | The regex to search for, targeting the **defect mechanism** (not the symptom text). The daemon compiles it with Go's stdlib `regexp` (RE2). |
| `note` | string | no | Optional context recorded alongside the search. |

The **daemon executes the search** — the agent supplies only the pattern; the hit
list is the daemon's recorded finding, which the agent can neither pad nor prune.
The walk is bounded (see [architecture.md](architecture.md)): it skips `.git`,
symlinks, non-regular files, files over 2 MiB, and files whose first 8 KiB contain
a NUL (binary sniff); it stops at **500 hits** and sets `truncated`; each excerpt is
trimmed to ~200 runes.

Rejections (each surfaced verbatim so the agent repairs rather than routes around):

- An **empty or uncompilable** pattern is a `400` carrying the compile error; no
  radius is stored.
- The **false-coverage gate** (`400`): the board must hold a `confirmed` hypothesis,
  and the confirmed culprit's probe file — the `file` of the probe cited in that
  hypothesis's confirm citation — must appear among the hits. A pattern that misses
  the known bug proves nothing about siblings, so the error names the culprit file
  and asks you to broaden the pattern. There is **no override**; a case whose fix
  was already applied simply skips the radius.

A re-run **replaces** the whole radius and clears every prior annotation.

**Result:** the stored radius plus its derived `unreviewed_count`:

```json
{
  "pattern": "toFixed\\(2\\)\\s*\\*\\s*100",
  "note": "float cents rounding before the discount multiply",
  "searched_at": "2026-07-10T18:03:11Z",
  "truncated": false,
  "hits": [
    { "file": "src/discount.js", "line": 42, "excerpt": "const cents = toFixed(2) * 100;" },
    { "file": "src/pricing.js", "line": 88, "excerpt": "return toFixed(2) * 100;" }
  ],
  "unreviewed_count": 2
}
```

- `hits` are cwd-relative `{file, line, excerpt}` — every hit the daemon recorded,
  in walk order. A freshly mapped hit carries no `verdict`/`note` until graded.
- `truncated` is `true` when the 500-hit cap was reached; every surface that renders
  the radius shows it. Narrow the pattern and re-run.
- `unreviewed_count` is how many hits carry no verdict yet (all of them, immediately
  after a search).

### `annotate_blast_radius`

Grade the recorded hits. Partial grading is valid; anything you leave alone stays
`unreviewed`.

| Param | Type | Required | Meaning |
|---|---|---|---|
| `session_id` | string | yes | The investigation. |
| `annotations` | object[] | yes | Per-hit verdicts to merge into the recorded radius. |

Each annotation entry:

| Field | Type | Required | Meaning |
|---|---|---|---|
| `file` | string | yes | The recorded hit's file, **exactly** as `map_blast_radius` returned it. |
| `line` | int | yes | The recorded hit's line. |
| `verdict` | string | yes | One of `sibling-bug`, `safe`, `already-covered`. |
| `note` | string | no | Optional rationale for the verdict. |

An invalid `verdict` is rejected **client-side** with the allowed set named, before
any daemon round-trip. The daemon then **set-checks** each `{file, line}` against the
recorded hits (paths compared normalized under the session `cwd`): an entry that
names a site the search never found rejects the **whole call** (`400`) with no
mutation — the agent cannot grade sites the search did not find. Annotating before
any radius exists is a `400`. Repeat annotations for the same `{file, line}`
overwrite (later verdict wins).

**Result:** the merged radius, same shape as `map_blast_radius`, with graded hits
now carrying their `verdict` and any `note`, and `unreviewed_count` reduced
accordingly:

```json
{
  "pattern": "toFixed\\(2\\)\\s*\\*\\s*100",
  "searched_at": "2026-07-10T18:03:11Z",
  "truncated": false,
  "hits": [
    { "file": "src/discount.js", "line": 42, "excerpt": "…", "verdict": "sibling-bug", "note": "same rounding, no integer-cents path" },
    { "file": "src/pricing.js", "line": 88, "excerpt": "…" }
  ],
  "unreviewed_count": 1
}
```

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
