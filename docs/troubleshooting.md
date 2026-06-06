# Troubleshooting

Organized by the symptom you observe. Find your symptom, work down its checklist.

> **Check the Health view first.** With the daemon running, open the Case Board's
> **Health** tab (`http://127.0.0.1:2218/#/health`). It answers "is sherlog healthy
> and what is it doing" at a glance — vitals and uptime, effective config with where
> each value came from, the data directory and its usage, recent activity, and self-
> checks (storage writable, loopback-only). A failing self-check is named in the
> status header. Many of the checks below are visible there without a terminal.

## `/debug` is unavailable / the MCP server fails to start

**Almost always: the `sherlog` binary is not on your PATH.** The plugin's MCP
server launches `sherlog mcp` from your PATH; if the binary is missing, the
server cannot start and no tools appear.

1. Confirm the binary is installed and on PATH:

   ```sh
   sherlog --version
   ```

   If this prints "command not found", install it (`brew install
   neomodular/tap/sherlog`) or add its location to PATH. The binary must be
   installed **before** the plugin can work — they version together.

2. Restart Claude Code so the plugin re-launches the MCP server now that the
   binary resolves.

## `await_run` returns zero events after I reproduced the bug

Zero events with a confirmed reproduction is a **connectivity or wiring**
problem, not evidence to kill a suspect. Walk these in order:

1. **Is the daemon up?** Check health:

   ```sh
   curl -s http://127.0.0.1:2218/health
   ```

   A JSON response with a `version` field means the sherlog daemon is answering.
   No response means nothing is listening — the next tool call auto-spawns it,
   but you can also confirm the port (see the port-conflict section). If the
   `version` field is missing, a **foreign process** holds the port (below).

2. **Did you actually rebuild / restart the app?** Compiled or bundled apps run
   *old* code until rebuilt. A probe you added to source is not running until the
   binary or bundle that contains it is rebuilt and restarted. Most "zero events"
   reports are an un-rebuilt app.

3. **Does the probe URL's session match the open session?** The probe line embeds
   a session token. If you reused a probe line from an earlier session (or
   hand-edited one), its `<session>` segment will not match the current open
   session, and the daemon drops the event silently (unknown sessions are
   discarded by design). Re-copy the `url_template` from `debug_start` / the
   current session and confirm the `/log/<session>/` prefix matches.

4. **Is the probe code path even executed?** Confirm the line you instrumented is
   actually on the path your reproduction exercises (a probe in an untaken branch
   never fires). A zero count for a *correctly wired* probe is real evidence; a
   zero count from any of the above is not.

If sherlog is up, the app is rebuilt, and the URL matches but events still do not
arrive, that is sherlog misbehaving — the skill may file a `report_observation`
field note.

## I upgraded sherlog but still see the old UI / old behavior

The daemon is a long-lived background process with the Case Board embedded in
its binary — upgrading the binary on disk does not touch the daemon that is
already running.

**From this release on, this fixes itself**: every tool call compares the
daemon's version against the binary's and transparently restarts the daemon on
mismatch. You only need a hard refresh in the browser (Cmd+Shift+R /
Ctrl+Shift+R) to drop cached board assets.

**One-time exception — the daemon you are replacing is ≤ 0.4.0.** Daemons that
old predate the restart mechanism, so the first tool call after upgrading fails
with instructions instead. Stop the old daemon once by hand:

```sh
pkill -f "sherlog daemon"          # macOS / Linux
```

```powershell
Get-Process sherlog | Stop-Process  # Windows
```

The next tool call spawns the new daemon automatically, and every future
upgrade self-heals without this step.

To confirm what is actually serving, compare:

```sh
sherlog --version                       # the binary on disk
curl -s http://127.0.0.1:2218/health    # the running daemon
```

> A restart that lands while another session is blocked in `await_run` cuts
> that wait after a ~2 s drain. Nothing is lost — investigation state is
> persisted — simply reissue the `await_run`.

## Port 2218 is already in use / "held by a process that is not the sherlog daemon"

The daemon binds `127.0.0.1:2218` (221B Baker Street). When a tool call finds the
port occupied by something that is not sherlog, it refuses to spawn into it and
returns:

> port 2218 is held by a process that is not the sherlog daemon — stop it or set
> SHERLOG_PORT to a free port (probe URLs follow the daemon's port automatically)

Two fixes:

- **Stop the foreign process** holding 2218, then retry the tool call (it
  auto-spawns the daemon).
- **Move sherlog to a free port** by setting `SHERLOG_PORT` (or the `port` config
  key — see [configuration.md](configuration.md)):

  ```sh
  export SHERLOG_PORT=2219
  ```

  Probe URLs are always generated from the template the daemon returns, so the
  new port propagates into every emitted probe line automatically — you do not
  edit probes by hand. `SHERLOG_PORT` takes precedence over a `port` in the
  config file.

  > Configuration applies on the **next daemon start** — there is no hot reload.
  > Stop the running daemon (or let the next tool call re-spawn it) after changing
  > the port.

## Leftover (stale) probes — finding and removing them

A probe registered but never marked removed is *stale*. Even when the session is
long gone, the safety net works without a running daemon (it reads the persisted
store directly):

```sh
sherlog probes --stale
```

It lists every stale probe across all sessions with its `SESSION`, `PROBE`,
`FILE`, and `LINE` so you can delete the orphaned line by hand. When nothing is
stale it prints `no stale probes — every registered probe has been removed`.

The browser **Case Board** (`http://127.0.0.1:2218/`) has the same view while the
daemon is running. During a normal close, `debug_end` already lists unremoved
probes and gives a greppable fragment to verify zero remain — `--stale` is the
weeks-later backstop.

## Where session data lives on disk

All sherlog state lives under `~/.sherlog/`:

| Path | Contents |
|---|---|
| `~/.sherlog/sessions/<id>/state.json` | One investigation: board, probe registry, runs, resolution. |
| `~/.sherlog/sessions/<id>/logs.jsonl` | Append-only probe events (and adoption markers) for that session. |
| `~/.sherlog/config.json` | Optional configuration (absent = all defaults). |
| `~/.sherlog/field-notes.jsonl` | Private `report_observation` field notes about sherlog itself. |

Read field notes with `sherlog notes` (add `--category tool-bug|friction|anomaly|other`
to filter); delete `~/.sherlog/field-notes.jsonl` to discard them. To reset an
investigation entirely, remove its `~/.sherlog/sessions/<id>/` directory while the
daemon is stopped.

> **Downgrade warning.** Do not run an older `sherlog` against state written by a
> newer build. Once a newer build writes an adoption marker into a session's
> `logs.jsonl`, an older event-only build mis-reads that marker as a phantom empty
> orphan. Stay on the newer build, or clear the session state before rolling back.

See [architecture.md](architecture.md) for how these files are read and written,
and [configuration.md](configuration.md) for tuning retention and flood control.
