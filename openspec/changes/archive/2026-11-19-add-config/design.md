# Design: add-config

## Context

MVP values are constants: flood keep-N in `internal/store/flood.go`, await debounce/clamp in `internal/daemon/await.go`/`server.go`, port in `internal/daemon/daemon.go` (env override only). The skill's presentation is fixed in `SKILL.md`. This change introduces one config source consumed at daemon startup and propagated outward.

## Goals / Non-Goals

**Goals:**
- One file, one CLI, strict precedence (env > file > default), zero new deps.
- Skill preferences travel through existing channels (`debug_start` response) — the plugin never reads the filesystem.
- Effective (post-precedence) config visible on `/health` for "why is it behaving like that" debugging.

**Non-Goals:**
- Hot reload (restart the daemon to apply; documented).
- Per-project/per-session overrides (global only, v1).
- Windows registry / XDG dirs beyond `~/.sherlog/` (consistent with session storage).

## Decisions

### D1: JSON file at ~/.sherlog/config.json, stdlib only
Same directory and format family as session state; `encoding/json` with a typed struct, unknown keys rejected (catch typos early via `DisallowUnknownFields`). Defaults expressed in one place (`config.Default()`); `Load()` returns defaults when the file is absent.

### D2: Precedence — env > file > default, resolved once at startup
Only `SHERLOG_PORT` exists as an env override today and it stays authoritative. Resolution happens once into an immutable `Effective` struct passed to daemon/store constructors — no global config reads sprinkled through the code (keeps testability; store already takes injected options).

### D3: CLI — `sherlog config list|get|set`
`set` validates against the schema (key exists, value parses, range checks: flood_keep 1–1000, debounce 0–30s, max timeout 30–3600s, retention_days ≥ 0) and writes atomically (temp+rename, same pattern as state.json). `list` prints effective values with their source (default/file/env) — the diagnosability win.

### D4: Skill preferences ride debug_start
`debug_start`'s response gains a `preferences {verbosity, color}` block. SKILL.md instructs: `minimal` = no mascot art, no detective vocabulary, plain status lines; discipline (suspects/probes/verdicts/cleanup gate) is identical — verbosity is presentation-only, never rigor. `color: never` strips ANSI.

### D5: Retention — prune closed sessions only, at startup + daily
With `retention_days > 0`, sessions closed longer than N days ago are deleted from disk and memory at daemon startup and every 24h. Open sessions are never pruned regardless of age. Pruning logs what it deleted (count, ids) to the daemon log. Recall (if case-board change is live) simply sees fewer closed cases — acceptable and documented; users valuing the archive set 0 (default).

## Risks / Trade-offs

- [Config typo silently ignored] → `DisallowUnknownFields` + `set` validation; `list` shows sources.
- [Retention deletes cases a user wanted] → default 0 (keep forever); deletion is logged; docs call it out.
- [Verbosity=minimal hides the Case Board link / cleanup confirmations] → minimal mode still prints functional lines (board URL, cleanup result) — only theming is dropped.
- [Two binaries (daemon vs MCP spawn) disagree on port] → both resolve through the same `config.Load()`; probe URL template always originates from the daemon, which is already the pattern.

## Migration Plan

Additive. No file = today's behavior. Constants become injected values with identical defaults; existing tests pin the defaults. Rollback = revert; an existing config.json is simply ignored by older binaries.

## Open Questions

None.
