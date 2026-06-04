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
