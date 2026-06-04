# Proposal: add-sherlog-mvp

## Why

Debugging with an AI agent today means dumping raw log files into the context window and hoping the agent remembers to clean up its debug statements. Cursor proved the hypothesis-driven debug loop works (generate hypotheses → instrument → user reproduces → analyze → refine → fix → verify), but its folder-based log sink can't debug browser code, can't filter or aggregate high-volume logs, and routinely leaves orphaned debug statements in code. Sherlog brings this loop to Claude Code with a resident local daemon that makes the loop strictly better: blocking waits instead of "tell me when done", queries instead of context-poisoning dumps, an investigation state that survives context compaction, and an infrastructure-level guarantee that every probe placed gets removed.

## What Changes

- New Go binary `sherlog` distributed via Homebrew: a single binary that runs as both a resident localhost daemon (HTTP log ingest + investigation state) and an MCP stdio server (engram-style dual mode).
- New Claude Code plugin containing the MCP server config and a `/debug` skill implementing the detective loop: hypotheses ("suspects"), one-line HTTP probes, blocking wait for user reproduction, run-based evidence analysis, iterative refinement, fix verification, and guaranteed probe cleanup.
- Probes are clean one-line HTTP calls inserted into user code (e.g. `fetch("http://127.0.0.1:2218/log/<session>/<probe>", {method:"POST", body: JSON.stringify({...})})`) — no SDK, no imports, no wrappers, no stdout capture. Works in any language including browser JavaScript (CORS-safe simple requests).
- Daemon persists per-session investigation state: bug description, hypothesis board, probe registry, runs with pass/fail verdicts — enabling `/debug resume` after compaction, crash, or days later, and stale-probe detection long after a session dies.
- Detective-themed identity: mascot (Clawd + inspector cap), port 2218 (Baker Street 221B), themed skill language ("the game is afoot" / "elementary").

## Capabilities

### New Capabilities

- `log-ingest`: Tolerant localhost HTTP endpoint that receives probe logs from any language/runtime (including browsers) and never breaks the host app — accepts any body, parses JSON opportunistically, CORS-safe, fire-and-forget semantics.
- `session-state`: Investigation lifecycle persisted in the daemon — sessions, hypothesis board, probe registry, runs with user verdicts; survives daemon restarts and outlives Claude sessions.
- `log-query`: Query-not-dump access to collected evidence — filter by session/run/probe/time, counts, first/last N, and a blocking `await` that suspends until logs arrive or timeout.
- `mcp-server`: MCP stdio mode of the same binary exposing the tool surface (`debug_start`, `register_probe`, hypothesis tools, `await_run`, `query_logs`, `debug_end`) to Claude Code, auto-starting/connecting to the daemon.
- `debug-skill`: The `/debug` skill — the detective loop discipline: ≥1 discriminating probe per hypothesis, kill/refine rules, repro/retest interaction, fix verification via probes, cleanup guarantee (grep for zero leftover probe URLs), branded banner and state lines.
- `distribution`: Build and release pipeline — goreleaser, Homebrew tap, plugin packaging/marketplace manifest, versioning between plugin and binary.

### Modified Capabilities

(none — greenfield project)

## Impact

- New repository contents: Go daemon source, Claude Code plugin (`.claude-plugin/` manifest, MCP config, skill), goreleaser config, Homebrew tap formula.
- New listening port on user machines: `127.0.0.1:2218` (localhost-bound only; random session path segments mitigate drive-by localhost POSTs).
- User code is temporarily modified during debug sessions (one-line probes); cleanup is part of the session contract.
- Dependencies: Go toolchain, goreleaser, MCP Go SDK (or hand-rolled stdio JSON-RPC), Homebrew tap repo.
- Platforms: macOS/Linux via brew at launch; Windows (scoop/winget) deferred post-MVP.
