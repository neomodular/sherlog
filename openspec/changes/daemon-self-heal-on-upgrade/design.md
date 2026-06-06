# Design: daemon-self-heal-on-upgrade

## Context

`brew upgrade` (and soon winget) replaces the binary on disk, but the resident daemon — detached, spawned once, UI embedded — keeps running the old build. `ensureDaemon` (`internal/mcp/client.go`) treats *any* healthy sherlog listener as "up": it reads `version` from `/health` solely to distinguish sherlog from a foreign process (D2 of the MVP design). There is no shutdown endpoint, no PID file, and `daemon.Run` blocks in `srv.Serve` with no way to stop the server other than killing the process. The binary and plugin version together (MVP D14), so a version mismatch between the MCP process and the daemon is always an upgrade (or downgrade) artifact — never a supported steady state.

## Goals / Non-Goals

**Goals:**

- After a binary upgrade, the next MCP tool call transparently replaces the stale daemon with the new build — zero user action.
- A graceful daemon stop primitive on the internal API surface, usable by the MCP client (and future tooling).
- A clear, actionable one-time error for daemons too old to know the shutdown endpoint.

**Non-Goals:**

- Daemon self-watching the binary on disk (polling `os.Executable` for changes) — the MCP-driven handshake covers the real trigger (a tool call) without background machinery.
- Authenticating `/api/` — the shutdown endpoint adopts the existing localhost trust boundary (any local process can already `kill` the daemon by PID). A token handshake is a separate, broader change if ever needed.
- Rescuing daemons ≤ 0.4.0 automatically — they lack the endpoint; they get the actionable error instead.
- In-place upgrade of investigation *state* — state lives in the store on disk and survives restarts by design (MVP D5/D6); nothing to migrate.

## Decisions

**D1 — Detection in `ensureDaemon`, comparing `/health` version to the MCP's own.**
The MCP process already calls `/health` on every ensure; `healthInfo.Version` is already parsed. `mcp.Run(ctx, version)` already receives the build version — it now threads into `newDaemonClient(version)` and the "sherlog is up" case becomes "up *and same version*". Both sides are stamped by the same `-ldflags` (release) or both read `dev` (local builds), so equality is the correct predicate; `dev` ↔ `dev` never restarts, and a `dev` ↔ release mismatch *should* restart (the binary on PATH is the source of truth). Alternative considered: only restart on daemon-older (needs semver parsing, and a downgrade leaving a *newer* stale daemon is just as wrong).

**D2 — Graceful stop via `POST /api/shutdown`, not PID kill.**
A PID kill needs port→PID discovery (lsof/netstat parsing or platform APIs — ugly cross-platform) or a PID in `/health` (which 0.4.0 daemons don't expose either, so it buys nothing for legacy). An endpoint is cross-platform, fits the existing internal `/api/` surface, and gives the daemon the chance to stop accepting work before exiting. The handler writes `200 {"ok":true}` first, then triggers shutdown asynchronously so the client always gets its ack. The Case Board remains strictly read-only — the board's JS never calls this endpoint; it lives with the other MCP-facing `/api/` routes.

**D3 — Server stop plumbing: a shutdown-request channel out of the handler, bounded drain in `Run`.**
`NewServer` gains a way to signal "shutdown requested" (a `chan struct{}` closed once, exposed to `daemon.Run`). `Run` selects on it in a goroutine: `srv.Shutdown(ctx)` with a ~2 s budget, then `srv.Close()`. The hard close matters: `await_run` long-polls can hold connections far past any polite drain window, and `Shutdown` waits for them. State is persisted at write time, so cutting an await is safe — the client reissues it (see Risks).

**D4 — Handshake order in `ensureDaemon` (mismatch path):**
1. `POST /api/shutdown`.
   - `404` → legacy daemon (≤ 0.4.0): return an actionable error naming the exact command — `pkill -f "sherlog daemon"` (or `Get-Process sherlog | Stop-Process` on Windows) — and stating it is one-time.
   - Connection error → daemon already exiting; proceed.
2. Poll until the port refuses a fresh dial (reuse the `portOccupiedByForeign` dialer logic, inverted) with a ~3 s deadline; if it never frees, fail with a clear error rather than spawning into a bind conflict.
3. Existing `spawnDaemon` + `waitForHealth`, unchanged.

Concurrent-restart race (two Claude sessions both detect the mismatch): both POST shutdown (second sees connection-refused, proceeds), both spawn — one child binds, the other child exits on `EADDRINUSE`, and both parents' `waitForHealth` see the healthy new daemon. No coordination needed.

## Risks / Trade-offs

- [Restart cuts a concurrent `await_run` from another session] → Bounded drain (2 s) then hard close; investigation state is already persisted, the await is simply reissued. Documented in troubleshooting. In practice the window is a single tool call immediately after an upgrade.
- [Any local process can POST /api/shutdown] → Same trust boundary as today (local processes can already `kill` the daemon; `/api/` already accepts unauthenticated localhost POSTs, e.g. session create). Worst case is a local DoS of a local debug tool; accepted and consistent with the documented security posture.
- [Users on ≤ 0.4.0 daemons don't self-heal] → Unavoidable (old binary lacks the endpoint); mitigated with the precise one-time error message and a troubleshooting entry.
- [Restart loop if spawn keeps producing a mismatched version] → Impossible by construction: the spawned child is `os.Executable()` of the MCP process itself, so its version equals the MCP's. `waitForHealth` failure surfaces as the existing spawn error.

## Migration Plan

Ships in the next release. First tool call after that upgrade hits the legacy 404 path once (manual kill); every subsequent upgrade self-heals. No data migration, no config, rollback is reverting the code.

## Open Questions

(none)
