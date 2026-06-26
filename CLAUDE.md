# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What sherlog is

A single Go binary (`github.com/neomodular/sherlog`) that powers hypothesis-driven
debugging for Claude Code. It ships as a Claude Code plugin: the `/debug` skill
drives a detective loop (name suspects → plant one discriminating probe each →
block while the user reproduces → eliminate suspects from evidence → fix → verify →
remove every probe). All investigation state lives in a resident localhost daemon,
not the conversation, so a case survives `/clear`, compaction, a crash, or
`/debug resume` days later.

## Commands

```sh
go build ./...                       # build everything
go vet ./...                         # must be clean (CI gate)
gofmt -l .                           # must print nothing (CI gate — gofmt-clean is enforced)
go test ./...                        # full suite
go test -race ./...                  # CI runs this; race check is authoritative on Linux
go install ./cmd/sherlog             # put your working copy on PATH

# Run one test / one package
go test -run TestName ./internal/store/
go test ./internal/daemon/...

# Full local validation suite (run before opening a PR)
go build ./... && go vet ./... && go test ./... && gofmt -l .

# Release dry-run (do NOT gate on `goreleaser check` — see .goreleaser.yaml header)
goreleaser release --clean --snapshot --skip=publish
```

The binary itself (useful for manual testing — these read `~/.sherlog` directly,
no daemon required):

```sh
sherlog daemon            # the resident localhost HTTP server (auto-spawned by mcp)
sherlog mcp               # the MCP stdio server the plugin launches
sherlog probes --stale    # list probes registered but never removed (cleanup safety net)
sherlog notes [--category tool-bug|friction|anomaly|other]
sherlog config list|get <key>|set <key> <value>
```

Testing the plugin against a working copy: `go install ./cmd/sherlog`, ensure that
binary is first on PATH, then **kill any running `sherlog` daemon process** so the
new binary auto-respawns on the next MCP call.

## Architecture

One binary, two runtime modes, plus the plugin skill. The data flow is:

```
probe (one-line HTTP POST) → daemon ingest → store (runs, flood control, adoption)
  → await/query/diff → MCP tools → /debug skill → fix → cleanup
```

- **`cmd/sherlog`** — hand-rolled subcommand dispatch over the stdlib `flag`
  package. **No CLI framework — this is a deliberate project rule.** `main.version`
  is injected at release via `-ldflags`.
- **`internal/store`** — the in-memory source of truth, persisted to disk. The
  daemon owns *all* investigation state (this is what makes resume possible). Holds
  the shared data model (`types.go`: Session, Hypothesis, Probe, Run, Resolution),
  flood control, pre-run adoption, persistence, and pub/sub.
- **`internal/daemon`** — the HTTP server. Three surfaces with different trust
  levels: public probe ingest (`/log/`, `/health`, permissive CORS for browsers),
  internal `/api/` (driven by the MCP process, no CORS), and a **GET-only**
  read-only Case Board at `/` (`ui/assets/`, embedded via `go:embed`, vanilla JS).
- **`internal/mcp`** — the stdio MCP server. **Holds no state**; every tool routes
  to the daemon's `/api/`. Before each call it ensures the daemon is up,
  auto-spawning a detached `sherlog daemon` if the port is silent (a foreign
  process on the port yields an actionable error instead).
- **`internal/config`** — config schema + precedence (`env > config.json > default`)
  + validation. `keys.go` is the single source of the key set.
- **`internal/notes`** — the field-notes channel (agent observations about sherlog
  itself → maintainer inbox).
- **`skills/debug/SKILL.md`** — the detective logic. It reasons **only** from the
  daemon board, never from conversation memory.

### Mechanisms worth understanding before touching the store/daemon

Read `docs/architecture.md` for the full treatment. The non-obvious ones:

- **Runs + await/debounce** — `await_run` opens (or atomically re-attaches to) a
  run and blocks watching the event counter (~10×/sec). It returns early once
  activity holds steady for the debounce window, or at timeout reporting zero. A
  zero-fill of *every registered probe* on return is load-bearing: `p3: 0` is the
  signal that kills the hypothesis `p3` discriminates and must be distinguishable
  from "no data".
- **Pre-run adoption** — events that arrive *before* a run opens are attributed to
  the new run (within a 15-min cap), but the `adopted` count is always disclosed so
  inferred evidence is visible. Adoption happens only at open, never on re-attach,
  and is persisted as a marker line *before* in-memory buffers change.
- **Flood control** — per `(run, probe)` storage keeps first-N + last-N events
  (`flood_keep`, default 20) plus an exact total; queries report `total`,
  `events`, and a `truncated` flag.
- **Persistence durability** — `state.json` is rewritten atomically (temp + fsync +
  rename); `logs.jsonl` is strictly append-only and replayed on restart. Config
  writes use the same atomic pattern.

### The fixed port

`2218` = 221B Baker Street. It's the brand and what makes a probe recognizable in a
diff. Override with `SHERLOG_PORT`; probe URL templates carry the resolved port so
overrides propagate automatically.

## Conventions

- **Dependencies: standard library only**, plus the official MCP Go SDK
  (`github.com/modelcontextprotocol/go-sdk`). A new dependency needs a strong case.
- **Docs must match the binary.** If a PR changes an MCP tool's schema, a config
  key, or a CLI flag, update the matching reference page in `docs/`
  (`tools-reference.md`, `configuration.md`) **in the same PR**. This is checked in
  review.
- **Design-decision references** — comments cite decisions as `D1`–`D14` (e.g.
  "the daemon owns investigation state (D6)"). The *why* behind each lives in the
  `openspec/changes/` records, not the code.
- **OpenSpec for substantial changes** — new capabilities or behavior changes are
  planned under `openspec/changes/<name>/` (proposal, design, delta specs, tasks).
  Look at archived changes there for house style. Small fixes just need a focused
  PR with a regression test.
- **Tests** — table-driven is the house style. Store tests use an **injectable root
  directory** (never write to the real `~/.sherlog`). Daemon tests use `httptest`
  or an ephemeral listener — **never assume port 2218 is free**. Bug fixes need a
  regression test.
- **Errors** wrapped with context (`fmt.Errorf("...: %w", err)`), never swallowed.
- **Commits**: Conventional Commits (`feat`, `fix`, `docs`, `test`, `refactor`,
  `chore`, `perf`, `ci`), with a scope matching the package where useful (e.g.
  `feat(store): ...`).

## Security invariants (PRs that weaken these will be declined)

- The daemon binds `127.0.0.1` only — never reachable off the machine.
- Session IDs are unguessable random tokens; events for unknown sessions are
  silently dropped.
- The Case Board is GET-only and embeds all assets (no external origins, no
  mutation through the browser).
- All data stays local under `~/.sherlog/`; nothing is ever uploaded — no telemetry.
