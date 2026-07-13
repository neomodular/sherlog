# Proposal: restart-on-upgrade

## Why

Upgrading sherlog today leaves a stale daemon squatting on port 2218: `brew upgrade` or `go install` replaces the binary on disk, but the resident daemon keeps serving the old behavior until someone kills it by hand. The manual dance — "reinstall the binary, **kill the resident daemon**, restart the session" — is literally a release-note instruction (v0.8.0), and every development iteration pays the same tax. The respawn half of the loop already exists: the MCP client auto-spawns `sherlog daemon` (resolved at spawn time) whenever the port is silent. What's missing is the yield: the old daemon never gets out of the way.

## What Changes

- **The daemon watches its own binary.** At startup it records the identity of `os.Executable()` (device/inode, mtime, size) and polls it on a background ticker (fixed ~30s, following the retention-pruning ticker precedent). When the file vanishes (brew cleanup removing the old Cellar path) or its identity changes (rename-over by `go install`/brew), the daemon concludes a new version landed.
- **Drain, then exit.** State is already persisted synchronously (atomic `state.json`, append-only `logs.jsonl`), so exit is safe at any instant — the drain exists only to avoid yanking a blocking `await_run` mid-reproduction. The daemon marks itself draining, exits once no await is in flight (a small in-flight gauge on the await engine, following the SSE `subscribers` atomic-gauge precedent), with a bounded fallback: exit anyway once the maximum await timeout has elapsed.
- **The next tool call completes the swap.** `ensureDaemon` finds the port silent and spawns the binary now on disk — the new version. Open runs replay as open on restart and `await_run` re-attaches; Case Board SSE clients reconnect via `EventSource` auto-retry.
- **Stale-MCP disclosure.** `ensureDaemon` logs an informational note when the daemon's `/health` version differs from the MCP client's own version — the half of an upgrade only a Claude session restart can fix. Informational only; no restart is triggered by version comparison.
- **Docs drop the manual step.** The "kill the resident daemon" upgrade instruction is deleted everywhere it appears; `architecture.md` gains the daemon-lifecycle section; `troubleshooting.md` documents the new upgrade flow.

Deliberately **not** version comparison between MCP and daemon: a stale MCP session comparing versions would downgrade or thrash a newer daemon, and `"dev"` builds are unorderable. The binary on disk is the single source of truth, and file identity works identically for releases and dev builds.

Out of scope: restarting the MCP process itself (Claude Code owns its lifecycle), any shutdown HTTP endpoint, version-ordering logic, config keys for the watcher.

## Capabilities

### New Capabilities

- `daemon-lifecycle`: the self-restart-on-upgrade behavior — executable identity watching, drain semantics, and the exit/auto-respawn handoff.

### Modified Capabilities

- `mcp-server`: ADDED — informational version-mismatch note in the daemon-ensure path; the auto-spawn loop documented as the second half of the upgrade handoff. No tool schema changes.

## Impact

- Code: `internal/daemon` (watcher goroutine, drain gauge on the await engine, clean exit path in `Run`), `internal/mcp` (version-mismatch note in `ensureDaemon`), `docs/architecture.md`, `docs/troubleshooting.md`, `README.md`/release-note guidance (manual kill instruction removed).
- No new dependencies, no new config keys, no on-disk format changes, no tool schema changes — fully backward compatible. A daemon that predates this change simply keeps the old behavior until its final manual kill.
