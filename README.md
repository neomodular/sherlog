<!-- Hero: terminal mascot sprite (D12) — coral Clawd-cousin "Watson" in a navy
inspector cap. The skill prints this colorized at session start; rendered plain
here. -->
```
     ▄▄▄▄
 ▄▄████████▄▄
   ▐▛███▜▌
  ▝▜█████▛▘
    ▘▘ ▝▝
```

# sherlog

Hypothesis-driven debugging for Claude Code. A single Go binary that runs as a
localhost daemon and an MCP server, plus a `/debug` skill that drives a
detective loop: name suspects, plant one discriminating probe each, wait while
you reproduce the bug, then eliminate suspects from real evidence — and remove
every probe before it calls the case closed.

## What it is

`sherlog` replaces "dump the logs into the chat and hope" with a structured
investigation:

- **Hypothesis-driven.** Claude commits to at least three distinct suspects up
  front, then places a probe per suspect whose output *distinguishes* that
  suspect from its rivals — not a "got here" marker.
- **Blocking wait.** When it is your turn to reproduce the bug, Claude suspends
  on `await_run` until probe activity arrives or it times out. No "type done
  when you're ready" — the daemon tells Claude when the evidence stops flowing.
- **Query, not dump.** Evidence enters Claude's context as filtered, aggregated
  query results (per-probe counts, first/last samples) — never raw log floods.
  A probe stuck in a hot loop is bounded to first/last N plus an exact counter.
- **Browser support.** Probes are plain HTTP POSTs with no JSON `Content-Type`,
  so browser JavaScript probes are CORS "simple requests" with no preflight.
  The same one-liner works in the browser, Node, Python, Go, Ruby, or a shell.

The investigation state — bug description, hypothesis board, probe registry, run
history — lives in the daemon, not in the conversation. It survives `/clear`,
context compaction, a crash, or picking the case back up days later via
`/debug resume`.

## Why

Folder-based AI debug loops can't reach browser code, can't filter high-volume
logs, and routinely leave orphaned debug statements behind. sherlog addresses
each of those at the infrastructure level: a resident daemon for browser-safe
ingest and querying, and a probe registry that makes every placed probe
findable and removable — even by a later session that has no memory of placing
it.

## Install

sherlog has two parts that version together: the binary (via Homebrew) and the
Claude Code plugin. **Install the binary first** — the plugin's MCP server
launches `sherlog` from your PATH.

1. **Binary** (Homebrew, macOS/Linux):

   ```sh
   brew install neomodular/tap/sherlog
   sherlog --version
   ```

2. **Plugin** (Claude Code): add this repository as a plugin marketplace source
   and install the `sherlog` plugin. The plugin ships `.mcp.json`, which launches
   `sherlog mcp`; once the binary is on PATH, `/debug` is available with no
   further configuration.

If the plugin's MCP server fails to start, the binary is almost certainly not on
PATH — run the `brew install` above. (Windows is supported for `go build`
development but is not yet packaged; brew covers macOS and Linux.)

## How it works

```
┌─────────────────────────────────────────────────────────────┐
│  your app (any language, including browser JS)              │
│  POST http://127.0.0.1:2218/log/<session>/<probe>           │
└──────────────────────────┬──────────────────────────────────┘
                           │ HTTP, loopback only
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  sherlog daemon                                             │
│  ingest │ session state │ hypothesis board │ runs │ query   │
└──────────────────────────┬──────────────────────────────────┘
                           │ same binary, `sherlog mcp` stdio mode
                           ▼
┌─────────────────────────────────────────────────────────────┐
│  Claude Code plugin: MCP tools + /debug skill (the brain)   │
└─────────────────────────────────────────────────────────────┘
```

The `sherlog mcp` process auto-spawns the daemon (detached) if port 2218 is not
already answering, so there is no separate daemon to start.

## The probe contract

A probe is **one fire-and-forget line** inserted into your code. The rules:

- **Never await it, never let it throw.** A down daemon must be silent; the probe
  must never block or break the host app.
- **Never set a JSON `Content-Type`.** Bodies go as default `text/plain` so
  browser probes stay CORS simple requests (no preflight). The daemon parses the
  body as JSON opportunistically and falls back to storing it as a raw string —
  a probe can never fail validation.
- **No new imports or wrappers** where the language allows a bare call. Put the
  discriminating values straight in the body.

`debug_start` returns the URL template and a canonical probe form per language —
one line where the language allows it, a short snippet where it does not. Swap
`<probe>` for the registered probe ID:

| Language | Probe form |
|---|---|
| JS (browser/Node) | `fetch("http://127.0.0.1:2218/log/<session>/<probe>", {method:"POST", body: JSON.stringify({/* values */})}).catch(() => {})` |
| Python (3-line snippet — `import`+`try` cannot share one physical line) | `import urllib.request, json` <br> `try: urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:2218/log/<session>/<probe>", data=json.dumps({}).encode())).close()` <br> `except Exception: pass` |
| Go | `go func(){ if r, err := http.Post("http://127.0.0.1:2218/log/<session>/<probe>", "", strings.NewReader("{}")); err == nil { r.Body.Close() } }()` |
| Ruby | `begin; require "net/http"; Net::HTTP.post(URI("http://127.0.0.1:2218/log/<session>/<probe>"), "{}"); rescue StandardError; end` |
| curl | `curl -s -X POST --data '{}' "http://127.0.0.1:2218/log/<session>/<probe>" >/dev/null 2>&1 &` |

## Security

sherlog is built for local debugging only, with defense in depth:

- **Loopback only.** The daemon binds `127.0.0.1:2218`; it is never reachable off
  the machine.
- **Unguessable session paths.** Any web page can POST to a localhost port, so
  the session segment of the probe URL is a random token generated per session.
  Without it, drive-by localhost POSTs go nowhere.
- **Unknown sessions are dropped.** Requests whose session segment matches no open
  session are silently discarded — no state is created from unsolicited traffic.

To override the port (e.g. 2218 is taken), set `SHERLOG_PORT`. Probe URLs are
always generated from the template the daemon returns, so the override
propagates into every emitted probe line automatically.

## Using /debug

A typical investigation:

1. **Open the case.** `/debug` — describe the bug (or let Claude ask one or two
   sharp questions). Claude calls `debug_start` and prints the banner.
2. **Name suspects.** Claude commits at least three distinct root-cause
   hypotheses to the board.
3. **Plant probes.** One discriminating probe per suspect, each registered with
   its file, line, and the hypothesis it tests.
4. **The game is afoot.** Claude asks you to reproduce the bug (rebuild/restart
   if the app is compiled or bundled) and suspends on `await_run`. For a slow
   reproduction, it just re-attaches to the same open run.
5. **Verdict.** You report `reproduced` / `not-reproduced`; Claude records it and
   reads the per-probe evidence summary, killing or refining suspects.
6. **Fix and verify.** Once one suspect is confirmed, Claude fixes it, runs a
   `fixed-check` reproduction, and confirms the failure signature changed as
   predicted before saying **"elementary."**
7. **Case closed.** `debug_end` lists every probe not yet removed. Claude deletes
   them and greps the repo for the session URL fragment, requiring zero matches
   before declaring **"case closed."**

### Resuming

`/debug resume` reconstructs an investigation from the daemon board — the latest
open session, or a named one. The board is the source of truth, so resume works
after `/clear`, compaction, a crash, or days later.

### Leftover probes, weeks later

Even if a session is long gone, the safety net is:

```sh
sherlog probes --stale
```

It lists every probe registered but never marked removed, across all sessions,
with the session, file, and line — so you can clean up orphans by hand.

## Port 2218

221B Baker Street — Sherlock Holmes's address. The fixed port is the brand and
what makes a sherlog probe instantly recognizable in a diff.

## License

MIT — see [LICENSE](LICENSE).
