# Architecture

How sherlog is put together and how its core mechanisms behave. Present tense
describes current behavior; the `openspec/` change records hold the history of
*why* each decision was made.

## Components

sherlog is a single Go binary that runs in two modes, plus a Claude Code plugin.

```
┌─────────────────────────────────────────────────────────────┐
│  your app (any language, including browser JS)               │
│  POST http://127.0.0.1:2218/log/<session>/<probe>            │
└──────────────────────────┬──────────────────────────────────┘
                           │ HTTP, loopback only
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  sherlog daemon  (`sherlog daemon`)                          │
│  ingest │ session store │ board │ runs │ query │ Case Board  │
│  public: /log/ /health    internal: /api/…    browser: / GET │
└──────────────────────────┬──────────────────────────────────┘
                           │ same binary, `sherlog mcp` stdio mode,
                           │ calls the daemon's /api/ over loopback
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Claude Code plugin: MCP tools + /debug skill (the brain)   │
└─────────────────────────────────────────────────────────────┘
```

- **Daemon** (`sherlog daemon`) — a resident localhost HTTP server. It exposes a
  public probe-ingest surface (`/log/`, `/health`, with permissive CORS so
  browser probes work), an internal `/api/` surface the MCP process drives (no
  CORS — server-side only), and a GET-only browser **Case Board** at `/`.
  - `/health` (public) is the frozen machine check: `{version, uptime, config}`.
  - `/api/stats` (browser-read, no CORS) is the human health aggregation behind
    the Case Board's Health view — vitals, effective config with sources, storage
    (data dir, disk usage, session/event/note counts), activity (last event,
    trailing-hour events, live SSE subscribers, open run), the stale-probe count,
    and boolean self-checks (`storage_writable`, `loopback_only`). It is separate
    from `/health` so that contract stays O(1) and unchanged.
- **MCP server** (`sherlog mcp`) — the stdio MCP server the plugin launches. It
  holds no investigation state; every tool call routes to the daemon's `/api/`.
  Before each call it ensures the daemon is up, auto-spawning a detached
  `sherlog daemon` if nothing answers on the port. A foreign process on the port
  produces an actionable error instead.
- **Store** — the in-memory source of truth inside the daemon, persisted to disk.
  The daemon owns all investigation state, which is what lets an investigation
  survive `/clear`, context compaction, a crash, or being resumed days later.
- **Plugin / `/debug` skill** — the detective logic. It reasons only from the
  daemon board, never from conversation memory.

The CLI adds out-of-band subcommands: `sherlog probes --stale`,
`sherlog notes`, `sherlog config`, and `sherlog --version`. The store-reading ones
work without a running daemon (they read `~/.sherlog` directly).

## Storage layout

All local state lives under `~/.sherlog/` (override the root in tests; production
uses the home directory):

```
~/.sherlog/
├── config.json                  # optional; absent = all built-in defaults
├── field-notes.jsonl            # report_observation notes about sherlog itself
└── sessions/
    └── <id>/
        ├── state.json           # board, probe registry, runs, resolution
        └── logs.jsonl           # append-only probe events + adoption markers
```

- **`state.json`** is rewritten atomically (temp file + fsync + rename) on every
  state change, so a crash mid-write leaves either the old or new version, never a
  half-written file.
- **`logs.jsonl`** is append-only: each probe event is one line, and a pre-run
  adoption is recorded as a separate marker line. On restart the daemon replays
  this file to rebuild in-memory buffers and re-apply adoption in order — no
  existing line is ever rewritten.

## Flood control

In-memory storage for each `(run, probe)` pair is bounded: the daemon keeps the
**first N** and the **last N** events plus an exact total counter, and drops the
middle. The default N is `flood_keep` (20). This caps memory under a probe stuck
in a hot loop while still disclosing the true volume and showing both ends of the
timeline.

A query or summary reports the true `total`, the retained `events` (first-N then
last-N when a gap opened), and a `truncated` flag whenever a middle was dropped.
Below `2N` events there is no gap, so every event is retained and de-duplicated
across the overlapping windows. Raise `flood_keep` for chatty apps; see
[configuration.md](configuration.md).

## Runs, await, and debounce

A *run* is a first-class reproduction attempt. `await_run` opens a run — or
**re-attaches** to the session's already-open run, atomically, so concurrent or
repeated `await_run` calls converge on one run rather than each opening their own.
The user's verdict (`close_run`) closes it.

`await_run` blocks and watches the run's total event count as a single cheap
activity signal (polled ~10×/sec; it reads only counters, never building
summaries):

- Once the count grows at least once (**first activity**), the call returns early
  as soon as the count holds steady for the **debounce window**
  (`await_debounce_seconds`, default 2s). This is how a finished reproduction
  returns promptly instead of waiting the full timeout.
- With **no activity at all** it returns at the timeout (default 120s, clamped to
  `await_max_timeout_seconds`, default 600s) reporting zero events, so the skill
  can run a connectivity check rather than wait forever.

On return, the summary lists **every registered probe**, zero-filling any that
never fired — a `p3: 0` is the signal that kills the hypothesis `p3` discriminates,
and it must be distinguishable from "no data".

## Pre-run adoption

A reproduction sometimes produces probe hits *before* the run is open (the user
reproduces, then the skill calls `await_run`). To attribute those correctly, when
a run opens the daemon **adopts** orphan events whose timestamps fall after the
previous run's boundary and within a 15-minute cap, attributing them to the new
run.

- Adoption happens **only at open**, never on re-attach.
- The adoption count is **disclosed**: every summary, query result, and diff side
  carries an `adopted` field, so evidence that was inferred from pre-run hits
  rather than seen live is visible. When `adopted == total` on a fixed-check run,
  the skill sanity-checks rather than trusting it blindly.
- Adoption is persisted as a marker line in `logs.jsonl` **before** in-memory
  buffers change, so memory and disk never disagree: a marker-write failure aborts
  the open with no adoption applied.

Adopted events also count as initial activity, so an already-complete
reproduction returns on the debounce window instead of the full timeout.

## Mechanical gates

The evidence layer is unfakeable by construction — events are daemon-ingested,
zero-filled, and adoption/truncation are disclosed. The *interpretation* of that
evidence used to live entirely in `skills/debug/SKILL.md` prose: the store would
accept a one-suspect board, a `confirmed` with no evidence, a `debug_end` whose
`confirmed_hypothesis_id` it never checked against the board, and a probe at any
file/line without looking. That asymmetry is what the **mechanical gates** close —
they move the loop's discipline rules from prose into the store and daemon.

**Threat model (this is what the gates are *for*).** They target **drift,
sloppiness, and hallucination** in an agent honestly running the loop — omission, a
malformed prediction pair, a fabricated citation, a prediction that was never
recorded. They do **not** target a deliberately deceptive agent: the agent writes
the probe code and could plant a lying probe, and no daemon check can beat that.
Any check whose only value would be defeating deception is deliberately out of
scope. The gates raise the floor for a drifting agent; they are not a security
boundary against the agent.

The gates come in three flavors, by what they catch:

- **Schema checks — omission and malformation.** Enforced at the point the value
  arrives. The board floor (`set_hypotheses` rejects fewer than three suspects),
  the prediction pair (`register_probe` requires `expected_if_true`/
  `expected_if_false` both-or-neither and non-equal), and the client-side
  requirement that a kill/confirm carry a `probe_id`+`run_id`.
- **Cross-checks against the store's own data — fabricated citations.** A kill or
  confirm's citation is validated against the session registry: the probe must be
  registered, and the run must exist **and be closed with a verdict** (enforcing
  ask-verdict-then-judge). A confirm additionally requires the session to hold ≥1
  run closed `reproduced` and the cited probe to carry predictions. A solved
  `debug_end` must name a `confirmed_hypothesis_id` that is actually `confirmed` on
  the board. The probe location check stats the resolved path so a probe can only
  be registered where a real source line exists.
- **Recorded-before-returned predictions — post-hoc rationalization.** The fix
  prediction is stamped on the run by `await_run(prediction=…)` at call receipt,
  **before** that call returns any summary, and is immutable once set.
  `close_run(fixed-check)` is rejected when the run carries no prediction. The
  fixed-check contrast is therefore judged against a claim recorded before the
  evidence was seen, not against conversation memory — the one place sherlog's
  founding rule says never to trust.

What each gate catches, and where it lives:

| Gate | Rejects | Lives in |
|---|---|---|
| Board minimum (D-E) | a board of fewer than three suspects; the prior board is left unchanged | `store.SetHypotheses` |
| Prediction pair (D-A) | one-sided or identical `expected_if_true`/`expected_if_false` | `store` probe registration |
| Probe location (D-G) | a `file` that does not exist under the session cwd, or a `line` past its end | `daemon` (owns the session cwd) |
| Hypothesis status enum | any status outside `active`/`killed`/`confirmed` — an unknown status would otherwise skip the citation gates and still mutate the board (found by the release dogfood) | `store.UpdateHypothesis` |
| Evidence citation (D-B) | a kill/confirm with no `probe_id`+`run_id`, an unknown probe, or an unknown/open run | `store.UpdateHypothesis` |
| Confirm gate (D-C) | a confirm with no `reproduced` run in the session, or a cited probe carrying no predictions | `store.UpdateHypothesis` |
| Fixed-check prediction (D-D) | a `fixed-check` close on a run with no recorded prediction | `store.CloseRun` |
| Solved close (D-F, D-J) | a partial resolution, a `confirmed_hypothesis_id` not confirmed on the board, or an out-of-enum guardrail type; the session stays **open** | `store.CloseSessionWithResolution` |
| False-coverage (blast-radius D-C) | a sibling search mapped with no `confirmed` hypothesis, or whose pattern misses the confirmed culprit's own file; no radius is stored | `store.SetBlastRadius` |

The internal `/api/` write surface also decodes **strictly**: an unknown JSON
field (a mistyped `predictions` key, say) is a `400`, never a silent drop that
leaves the caller believing state was recorded. The public `/log/` probe ingest
stays deliberately permissive (D3) — a probe can never fail validation.

Every gate is a typed sentinel wrapped with a one-line, actionable repair
instruction (D-K). The daemon maps the sentinel to a `400` (an unknown cited
probe/run is a `404` — the cited thing is genuinely absent) and writes the message
to the body verbatim; the MCP tool surfaces it unaltered so the agent repairs the
board rather than routing around the error. A rejection is **never** a silent
downgrade — a failed solved close leaves the session open, not quietly unsolved.

**The known loophole (D-D, accepted).** An agent that calls `await_run` *without* a
prediction, reads the partial evidence, then re-attaches and supplies a prediction
has technically seen data before "predicting". Predictions are immutable **once
set** but adoptable on a re-attach whose run has none, so this path exists. It is
accepted per the threat model: the realistic failure — never predicting at all — is
caught, and closing the loophole would require a run-discard concept that is not
worth its weight.

**A second loophole (D-C, accepted).** The confirm gate checks the cited probe's
*current* prediction pair, and `register_probe` updates an existing probe ID in
place — so an agent whose confirm was rejected for a prediction-less probe can
re-register that probe with a post-hoc pair and immediately confirm citing the
already-closed run: predictions authored after the evidence was seen. Accepted for
the same reason as D-D — the gate's real job (no confirm without *any* recorded
discriminating claim) still holds, and closing it would require freezing probes at
run close. The skill's repair path ("register the predicted probe, **rerun**, cite
the new evidence") remains the honest route; the binary does not force the rerun.

The commit SHA (`git -C <cwd> rev-parse HEAD` at `debug_start`,
silently omitted outside a git tree) and the computed repro rate (`reproduced /
(reproduced + not-reproduced)` over closed runs, fixed-check excluded, never
stored) are **recording/signal only** — no gate consumes them beyond D-C's "≥1
reproduced run". All new state fields are additive; a pre-change `state.json` loads
and resumes with them absent.

### The blast-radius read surface

The gates above harden the loop through the confirm; the blast radius extends the
same trust model *past* it. A confirmed root cause rarely stands alone — the same
anti-pattern usually lives at sibling call sites, and "I grepped for other
occurrences" is exactly the unrecorded, unrunnable claim sherlog refuses elsewhere.
`map_blast_radius` replaces it with a search the **daemon executes itself** (D-A).

This is the first place the daemon reads user file **contents**. Until now it only
*stats* paths — the probe-location gate checks that a file/line exists. The sibling
search compiles the agent's pattern with Go's stdlib `regexp` (the RE2 engine —
linear-time matching, so an untrusted pattern cannot wedge the daemon) and walks the
session `cwd` with `filepath.WalkDir`, recording each match as `{file, line,
excerpt}`. The security invariants hold: the read is **read-only**, rooted at the
session `cwd`, and the hits land in `state.json` under `~/.sherlog/` like everything
else — nothing leaves the machine. The walk runs **outside the store lock** (D-G),
so a long scan never blocks `/log/` ingest or an open `await_run`.

**Why daemon execution beats verify-after.** The obvious alternative would let the
agent report a hit list and have the daemon *check* it by re-running the query and
comparing sets. Daemon execution is strictly stronger: there is no agent-supplied
set to compare, so under-reporting a real sibling and inventing a fake one are
**impossible by construction**, not detected after the fact. The agent contributes
only two things — the pattern and a verdict per hit — and neither can add or remove
a hit from the recorded set.

**Scan bounds (D-B), mirroring flood control — bound the work, always disclose the
bound.** The walk skips `.git` wholesale, never follows symlinks (a symlinked file
could point outside the cwd), skips non-regular and oversized files (over **2 MiB**),
and skips any file whose first **8 KiB** contain a NUL byte (binary sniff). It stops
at **500 hits** and sets a `truncated` flag that every surface displays; each excerpt
is trimmed to ~200 runes so a minified match cannot bloat `state.json`. An unreadable
entry is skipped, never fatal — a permission-denied subtree does not abort the scan.

**The false-coverage gate (D-C).** `map_blast_radius` is rejected unless the board
holds a `confirmed` hypothesis, and rejected when the confirmed culprit's file — the
`file` of the probe cited in that hypothesis's confirm citation (the citation
`harden-detective-gates` introduced) — is **absent from the hit set**. A pattern that
does not even match the known bug cannot establish anything about its siblings; the
rejection names the culprit file. There is deliberately **no override**: the skill
runs the search after the confirm and *before* the fix, while the anti-pattern still
exists at the culprit, and an escape hatch for "the culprit was already fixed" is
precisely the bypass a drifting agent would reach for. A case resumed post-fix simply
proceeds without a radius — it is optional and never gates `debug_end`.

Annotations are set-checked the same way: `annotate_blast_radius` rejects any
`{file, line}` not among the recorded hits (paths compared normalized under the
session `cwd`), so the agent cannot grade a site the search never found. A re-run
replaces the whole radius (D-E), clearing prior verdicts that graded a different
search. The radius rides an **additive** `state.json` field (`blast_radius`); a
pre-change session loads and resumes with it absent.

## Live event stream

The store publishes typed change events (`log`, `board`, `run`, `probe`) to
subscribers. The Case Board's Server-Sent Events endpoint bridges these to the
browser so the evidence tail streams live during a reproduction. Subscribers are
independent and non-blocking: a stalled browser is dropped rather than stalling
ingest or other subscribers.

## Daemon discovery and the port

The MCP process and the daemon resolve the port through the same configuration
path (`SHERLOG_PORT` → `config.json` `port` → built-in `2218`), so they never
disagree on where to talk. Probe URL templates carry the resolved port, so a port
override propagates into every emitted probe line. See
[configuration.md](configuration.md) for precedence details and
[troubleshooting.md](troubleshooting.md) for port conflicts.
