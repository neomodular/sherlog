# Tasks: add-sherlog-mvp

## 1. Project Scaffolding

- [x] 1.1 Initialize git repo + Go module (`go.mod`), directory layout: `cmd/sherlog/` (main with `daemon`/`mcp`/`probes`/`--version` subcommands), `internal/daemon/`, `internal/store/`, `internal/mcp/`, plugin dir (`.claude-plugin/`, `.mcp.json`, `skills/debug/`)
- [x] 1.2 Wire the official MCP Go SDK (`modelcontextprotocol/go-sdk`) per design D13
- [x] 1.3 Define core types in `internal/store/`: Session, Hypothesis, Probe, Run, LogEvent (per design D5–D7)

## 2. Store (session-state)

- [x] 2.1 Implement in-memory store with atomic `state.json` writes and `logs.jsonl` appends under `~/.sherlog/sessions/<id>/`
- [x] 2.2 Implement startup recovery: replay `state.json` + `logs.jsonl` for all sessions (spec: state survives daemon restart)
- [x] 2.3 Implement session lifecycle: create (random ≥8-char base36 ID, cwd, description), close, same-cwd open-session detection
- [x] 2.4 Implement hypothesis board mutations (set/update with status + evidence notes) and probe registry (register/remove flags)
- [x] 2.5 Implement flood control: per probe per run keep first/last N (default 20) + exact counter
- [x] 2.6 Unit tests for store: lifecycle, recovery, flood control, concurrent session detection

## 3. Daemon HTTP Server (log-ingest)

- [ ] 3.1 Implement `sherlog daemon`: loopback-only listener on 2218 with `SHERLOG_PORT` override; fail fast with clear message if the port is held by a foreign process
- [ ] 3.2 Implement `POST /log/<session>/<probe>`: tolerant body parsing (JSON attempt → raw string fallback, empty OK), always-200 for routed requests, drop unknown sessions silently, <50ms response
- [ ] 3.3 Implement CORS: `Access-Control-Allow-Origin: *` on responses + OPTIONS preflight handling
- [ ] 3.4 Implement `GET /health` (version, uptime) and internal API endpoints for MCP-process operations (session CRUD, board, query, await)
- [ ] 3.5 Implement the await/long-poll engine: open-or-attach run, quiet-after-activity debounce (~2s), timeout return, run close with verdict
- [ ] 3.6 Implement query + per-run probe summary endpoints (counts, first/last samples, truncation disclosure)
- [ ] 3.7 Integration tests: browser-style simple-request POST (no preflight), flood, await debounce/timeout/re-attach, unknown-session drop

## 4. MCP Server (mcp-server)

- [ ] 4.1 Implement `sherlog mcp` stdio mode with server info (version) and tool registration
- [ ] 4.2 Implement daemon health-check + detached auto-spawn on startup/first tool call; foreign-port error path
- [ ] 4.3 Implement tools: `debug_start` (returns session ID, probe URL template, canonical per-language probe one-liners), `debug_resume`, `debug_end` (cleanup checklist + greppable fragment)
- [ ] 4.4 Implement tools: `set_hypotheses`, `update_hypothesis`, `register_probe`, `remove_probe`
- [ ] 4.5 Implement tools: `await_run` (default 120s, re-invocable/re-attaching), `close_run`, `query_logs`
- [ ] 4.6 End-to-end test: MCP handshake → debug_start → simulated probe POSTs → await_run → query → debug_end via a scripted client

## 5. CLI Utilities

- [ ] 5.1 Implement `sherlog probes --stale` (unremoved probes across all sessions with session/file/line) and `sherlog --version`

## 6. Plugin + Skill (debug-skill)

- [ ] 6.1 Write `.claude-plugin/plugin.json` and `.mcp.json` (launches `sherlog mcp`); validate plugin loads in Claude Code
- [ ] 6.2 Write `skills/debug/SKILL.md`: full detective loop per spec — ≥3 suspects, discriminating-probe rule, probe one-liner forms per language (fire-and-forget, no imports/wrappers, no JSON content-type), await/verdict interaction, zero-event connectivity check, kill/refine rules, fix verification via `fixed-check` run, cleanup gate (remove + grep `2218/log/<session>` = zero matches), `/debug resume` flow, same-cwd warning
- [ ] 6.3 Add branded presentation to the skill: locked mascot sprite (exact Clawd glyphs + 2-row inspector cap), navy/coral ANSI colors with no-color fallback, status-line format, detective vocabulary ("the game is afoot" / "elementary." / "case closed")
- [ ] 6.4 Manual end-to-end dogfood: seed a bug in a sample Node app and a sample browser page, run `/debug` through repro → root cause → fix → verified cleanup on both

## 7. Distribution (distribution)

- [ ] 7.1 goreleaser config: pure-Go static builds for darwin/arm64, darwin/amd64, linux/arm64, linux/amd64; version injected via ldflags
- [ ] 7.2 Homebrew tap repo + formula publication from goreleaser; verify `brew install <org>/tap/sherlog && sherlog --version`
- [ ] 7.3 Release workflow (tag → build → tap update) in CI
- [ ] 7.4 README: install (brew + plugin), how it works diagram, probe contract, security notes (loopback-only, unguessable session paths), mascot
- [ ] 7.5 Marketplace packaging/manifest for the plugin with brew prerequisite documented
