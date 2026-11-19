# Design: restart-on-upgrade

## Context

The upgrade handoff is half-built. `ensureDaemon` (internal/mcp/client.go) already health-checks the port and, when silent, spawns `sherlog daemon` from an executable path resolved *at spawn time* — so a fresh spawn always runs the newest binary on disk. But the daemon is a detached resident process with no reason to ever exit: after `brew upgrade` or `go install`, the old process keeps answering `/health`, so `ensureDaemon` happily keeps talking to stale behavior. Every upgrade (and every dev iteration) requires a manual `pkill`.

Durability makes this fixable cheaply: the store persists synchronously (atomic `state.json` rewrite per change; append-only `logs.jsonl` replayed on start), so a daemon exit at *any* moment loses nothing — this was demonstrated repeatedly during the v0.8.0 release work, where the daemon was killed and respawned four times mid-investigation with zero loss.

## Goals / Non-Goals

**Goals:**

- A new binary on disk causes the resident daemon to exit on its own within one watch interval, without interrupting an in-flight reproduction wait.
- The swap completes with zero user action beyond the upgrade itself: the next tool call auto-spawns the new version.
- Works identically for brew releases and `go install` dev builds.

**Non-Goals:**

- Restarting the MCP process (Claude Code owns its lifecycle; we only *disclose* the mismatch).
- A shutdown/restart HTTP endpoint (nothing needs to command the daemon; it observes the disk).
- Version parsing or ordering of any kind.
- Configurability of the watcher (fixed interval; YAGNI until field data says otherwise).

## Decisions

### D-A · File identity, not version comparison

The daemon records `os.Executable()` at startup and stats it on a ticker, comparing **device+inode, mtime, and size**. A vanished file (brew cleanup deletes the old Cellar directory) or changed identity (rename-over, the atomic install pattern of both brew and `go install`) means a new binary landed. Version strings are never compared.

*Alternative considered:* `ensureDaemon` compares its own version to the daemon's `/health` version and restarts on mismatch. Rejected — a stale MCP process (spawned before the upgrade, still running in an open Claude session) would *downgrade* a newer daemon, and two concurrent sessions on different binaries would thrash the daemon in a restart loop. `"dev"` vs `"dev"` is also unorderable exactly where the pain is worst (development). The disk is the single source of truth; the observer with the stale copy never gets a vote.

### D-B · Watcher shape

A goroutine in `daemon.Run`, following the retention-pruning ticker precedent: fixed `binaryWatchInterval = 30s` constant, stat via `os.Stat` + `syscall.Stat_t` for device/inode (with a portable fallback to mtime+size where the syscall view is unavailable). The recorded identity is captured **before** the listener opens so a binary replaced during startup is still caught on the first tick. An `os.Executable()` failure at startup disables the watcher for that process (logged once) — never a fatal error; the old manual behavior simply remains.

### D-C · Drain: exit only between reproductions, with a bounded fallback

Exit is safe at any instant (durability, above), but killing a *blocking `await_run`* mid-reproduction is rude: the tool call errors while the user is busy reproducing. So the await engine gains an in-flight gauge (`atomic.Int64`, following the `Server.subscribers` precedent): on trigger the daemon marks itself draining and exits on the first watch tick where the gauge reads zero. Bounded fallback: if draining exceeds `await_max_timeout_seconds` (the longest any await can block), exit regardless — a wedged await must not pin a stale binary forever.

New awaits arriving *while draining* are still served — rejecting them would break an active investigation for an invisible reason, and the fallback bounds the total drain. The interrupted-await worst case is recoverable by design: the run replays as open, `await_run` re-attaches on the next call.

### D-D · Exit path and what the user sees

The drain trigger logs one line (stderr — visible in `nohup`/launchd logs) naming the old and observed binary states, then `daemon.Run` returns cleanly (exit 0) after the drain: no `os.Exit` mid-handler, listeners closed via the existing shutdown path. The next MCP tool call finds the port silent, spawns the new binary, and the investigation continues off the replayed state. Case Board browsers reconnect on their own: `EventSource` auto-retries, and the case list re-fetches on reconnect.

### D-E · Stale-MCP disclosure, not enforcement

After a successful health check, `ensureDaemon` compares `info.Version` to its own compiled version; on mismatch it writes a single informational line to stderr (once per process, not per call): the daemon is current, the MCP process is not, and only a session restart loads the new tool schemas. No behavior changes — this is the honest boundary of what sherlog can fix from inside a running session.

## Risks / Trade-offs

- [A `touch`/re-sign of the binary restarts the daemon spuriously] → Harmless by design: state survives, next call respawns; a false-positive restart costs one auto-respawn.
- [Long-running reproduction blocks the swap up to the max await timeout] → Intentional; the fallback bounds it, and the stale window was previously *infinite*.
- [Device/inode semantics vary across filesystems (network mounts, exotic setups)] → mtime+size fallback still catches rename-over installs; a missed edge case degrades to today's behavior (manual kill), never to a crash.
- [Two daemons racing during the swap (old exiting, new spawning)] → Already handled: the bind on 127.0.0.1:2218 is exclusive; the spawn path treats a bind failure with a clear error, and `ensureDaemon` only spawns after the port stops answering.
- [Watcher goroutine leaks in tests] → The watcher takes an injectable stop channel/context and an injectable path+interval; tests drive it synthetically and never watch the test binary.

## Migration Plan

1. Pure addition: no schema, disk, or tool changes. Ships in the next release.
2. The *first* upgrade to a version carrying this change still needs one last manual daemon kill (the resident daemon predates the watcher). Release notes say exactly that, once, and the instruction dies thereafter.
3. Rollback = previous release; nothing to migrate back.

## Open Questions

(none — all decisions above are settled for this change)
