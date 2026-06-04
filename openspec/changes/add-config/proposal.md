# Proposal: add-config

## Why

Every tuning value in sherlog is currently hardcoded (flood-control N=20, await debounce 2s, await clamp 600s, port via env var only) and the `/debug` skill has exactly one personality — full detective narrative. Users debugging chatty apps need bigger flood windows; users who find the theming noisy need a terse mode; none of this should require recompiling.

## What Changes

- New config file `~/.sherlog/config.json` with CLI management: `sherlog config list|get <key>|set <key> <value>`.
- Precedence: environment variable > config file > built-in default (`SHERLOG_PORT` keeps working).
- Daemon knobs: `port`, `flood_keep` (first/last N), `await_debounce_seconds`, `await_max_timeout_seconds`, `retention_days` (0 = keep forever; otherwise closed sessions are pruned).
- Skill knobs: `verbosity` (`detective` | `minimal`), `color` (`auto` | `always` | `never`) — delivered to the skill through the `debug_start` response so the plugin needs no separate config read.
- `/health` and the Case Board surface the effective config for diagnosability.

## Capabilities

### New Capabilities

- `configuration`: Config file, CLI subcommand, precedence rules, validation, and propagation of effective settings to daemon behavior and skill presentation.

### Modified Capabilities

- `debug-skill`: ADDED — honors `verbosity` and `color` preferences from the `debug_start` payload (`minimal` drops the theater, keeps the discipline).

## Impact

- Code: new `internal/config` package; `cmd/sherlog` (config subcommand); `internal/daemon` (consume knobs, expose effective config); `internal/store` (retention pruning, flood N injection — flood is currently a constant); `internal/mcp` (config in debug_start payload); `skills/debug/SKILL.md`.
- Backward compatible: missing config file = current defaults; all keys optional.
- Retention introduces the first deletion path in sherlog — pruning must never touch open sessions.
