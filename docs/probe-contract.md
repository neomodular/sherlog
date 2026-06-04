# Probe Contract

A *probe* is one fire-and-forget line you insert into your code. It POSTs a small
JSON snapshot to the sherlog daemon, which buckets it by session and probe ID so
the investigation can query it later. This page is the canonical reference for
what a probe line looks like and the rules every probe must obey.

You rarely write these by hand: `debug_start` returns the exact URL template and a
copy-pasteable one-liner per language. This page documents what it emits and why.

## The three rules

1. **Fire-and-forget — never await it, never let it throw.** A probe runs on the
   host app's hot path. If the daemon is down, the call must fail silently: no
   blocking, no exception escaping into your app. Every one-liner below swallows
   its own errors.
2. **Never set a JSON `Content-Type`.** Bodies go as the default `text/plain`.
   This keeps a browser `fetch` probe a CORS [*simple request*](https://developer.mozilla.org/en-US/docs/Web/HTTP/CORS#simple_requests)
   with no preflight `OPTIONS` round-trip. The daemon parses the body as JSON
   opportunistically and stores it as a raw string when it does not parse — so a
   probe can never fail validation regardless of content type.
3. **No new imports or wrappers** where the language allows a bare call. Put the
   discriminating values straight in the body. A probe is a measurement, not a
   logging framework.

## URL anatomy

Every probe POSTs to the same shape:

```
http://127.0.0.1:2218/log/<session>/<probe>
└──────┬──────┘ └─┬─┘ └──┬──┘ └───┬──┘ └──┬──┘
   loopback     port  fixed   session  probe
   host                route   token    ID
```

- **`127.0.0.1:2218`** — the daemon, loopback only. The port follows
  configuration: if `SHERLOG_PORT` (or a `port` config key) changes it, the URL
  template `debug_start` returns carries the new port automatically, so every
  emitted probe line is correct without manual edits.
- **`/log/`** — the fixed public ingest route.
- **`<session>`** — a random per-session token (short base36). It is
  unguessable, so drive-by POSTs from a random web page to `localhost:2218` go
  nowhere: requests whose session segment matches no open session are dropped
  silently.
- **`<probe>`** — the registered probe ID (`p1`, `p2`, …). You substitute this
  per probe so each suspect's measurements land in their own bucket.

The path must be exactly two segments after `/log/`. Extra segments are rejected
(`404`), so a typo never silently creates a phantom bucket.

## One-liner per language

Swap `<session>` and `<probe>` for the real values (the template `debug_start`
returns already has the session filled in — you only replace `<probe>`). Put the
values that *distinguish this suspect from its rivals* in the body instead of the
empty `{}`.

### JavaScript (browser and Node 18+)

```js
fetch("http://127.0.0.1:2218/log/<session>/<probe>", {method:"POST", body: JSON.stringify({/* values */})}).catch(() => {})
```

`fetch` defaults the body to `text/plain` (no preflight in the browser); the
trailing `.catch(() => {})` makes a down daemon silent.

### Python (3.x, stdlib only)

`import` and `try` cannot share one physical line, so this is a three-line
snippet rather than a one-liner:

```python
import urllib.request, json
try: urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:2218/log/<session>/<probe>", data=json.dumps({}).encode()))
except Exception: pass
```

Uses `urllib` from the standard library, so no `requests` dependency is assumed.
The daemon ignores the absent content type.

### Go

```go
go func(){ if r, err := http.Post("http://127.0.0.1:2218/log/<session>/<probe>", "", strings.NewReader("{}")); err == nil { r.Body.Close() } }()
```

Runs in a goroutine so it never blocks the host path. The empty content-type
argument keeps the body `text/plain`. Requires `net/http` and `strings` already
imported.

### Ruby

```ruby
begin; require "net/http"; Net::HTTP.post(URI("http://127.0.0.1:2218/log/<session>/<probe>"), "{}"); rescue StandardError; end
```

`Net::HTTP.post` with a string body sends `text/plain`; the `rescue` makes a down
daemon silent.

### curl (shell repro scripts)

```sh
curl -s -X POST --data '{}' "http://127.0.0.1:2218/log/<session>/<probe>" >/dev/null 2>&1 &
```

`--data` sends `text/plain`; `&` backgrounds it; the redirects silence it.

## Greppability

The fixed `/log/<session>/` prefix is what makes every probe instantly findable
in a diff or a code search. Two consequences:

- **In review**, a `127.0.0.1:2218/log/` substring in a diff is a sherlog probe —
  recognizable on sight, hard to merge by accident.
- **At cleanup**, `debug_end` returns a `greppable_fragment` —
  `http://127.0.0.1:2218/log/<session>/` for the session being closed. Grepping
  the repo for it and requiring **zero matches** proves no probe of that session
  was left behind. The fixed port (221B Baker Street) exists precisely so this
  fragment is stable and unmistakable.

See [troubleshooting.md](troubleshooting.md) for diagnosing probes that fire zero
events, and [tools-reference.md](tools-reference.md) for `debug_start`,
`register_probe`, and `debug_end`.
