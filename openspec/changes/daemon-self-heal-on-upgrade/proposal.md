# Proposal: daemon-self-heal-on-upgrade

## Why

The daemon is a long-lived detached process with the Case Board UI embedded in its binary. A brew/winget upgrade swaps the binary on disk but never touches the running daemon, and `ensureDaemon` accepts any healthy sherlog listener — so after every upgrade, users silently keep the old daemon (old UI, old behavior, old bug they upgraded to fix) until they discover `pkill` on their own. This hit the first real upgrade (0.3.0 → 0.4.0) within hours.

## What Changes

- New **`POST /api/shutdown`** daemon endpoint: acknowledges with 200, then gracefully stops the HTTP server so the process exits. Internal `/api/` surface only — the Case Board stays strictly read-only.
- **Version-mismatch self-heal in `ensureDaemon`**: the MCP client already reads `version` from `/health`; it now compares it against its own build version. On mismatch it shuts the old daemon down via the new endpoint, waits for the port to free, respawns through the existing detached-spawn path, and waits for health — the upgrade is invisible to the user.
- **Legacy fallback**: a pre-shutdown-endpoint daemon (≤ 0.4.0) answers the shutdown call with 404. The tool call then fails with a one-time actionable error naming the exact manual command (`pkill -f "sherlog daemon"` / Stop-Process on Windows). Every upgrade after this release self-heals without it.
- Docs in the same change per repo convention: troubleshooting gains an "after upgrading" entry; architecture's lifecycle/endpoint sections document the shutdown handshake.

## Capabilities

### New Capabilities

(none)

## Modified Capabilities

- `log-ingest`: ADDED — the `POST /api/shutdown` graceful-stop endpoint on the daemon's internal API surface.
- `mcp-server`: ADDED — version-mismatch detection in daemon ensure-up, the shutdown→respawn handshake, and the legacy-daemon actionable error.

## Impact

- Code: `internal/daemon` (shutdown endpoint, server stop plumbing), `internal/mcp` (client version, mismatch handshake in `ensureDaemon`), `docs/troubleshooting.md`, `docs/architecture.md`.
- No tool schema changes, no config keys, no new dependencies; `/health` response unchanged.
- Trust boundary unchanged: any local process could already kill the daemon by PID; the endpoint stays loopback-only like everything else.
- Edge accepted (documented in design): a restart triggered while another client long-polls `await_run` drops that wait; state is persisted, so the await can simply be reissued.
