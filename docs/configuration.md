# Configuration

Every tuning value has a built-in default, so configuration is entirely optional —
an absent config file reproduces sherlog's default behavior exactly. Configure
sherlog by editing `~/.sherlog/config.json` or using the `sherlog config` CLI.

## Keys

| Key | Default | Range / values | Effect |
|---|---|---|---|
| `port` | `2218` | 1–65535 | Daemon TCP port (loopback only). `SHERLOG_PORT` overrides it. |
| `flood_keep` | `20` | 1–1000 | First/last-N events retained per probe per run; the middle is dropped with the exact total still disclosed. Raise it for chatty apps. |
| `await_debounce_seconds` | `2` | 0–30 | How long probe activity must stay quiet before `await_run` returns early. |
| `await_max_timeout_seconds` | `600` | 30–3600 | Upper clamp on an `await_run` timeout. Also bounds the default 120s wait, so setting it below 120 shortens default awaits too. |
| `retention_days` | `0` | ≥ 0 | Prune closed sessions older than N days. `0` keeps everything forever. |
| `verbosity` | `detective` | `detective` \| `minimal` | Skill presentation: `minimal` drops the mascot and vocabulary, keeps the discipline. |
| `color` | `auto` | `auto` \| `always` \| `never` | ANSI color in the skill banner. `never` strips all escapes. |

Keys are **strict**: an unknown key (e.g. a typo like `flod_keep`) fails loading
with a clear error rather than being silently ignored. An out-of-range value also
fails loading loudly at startup.

## Precedence

Highest wins:

```
environment variable  →  config file (~/.sherlog/config.json)  →  built-in default
```

Only one environment override exists today — **`SHERLOG_PORT`** — and it stays
authoritative over a `port` set in the file. Everything else is file-or-default.
`config list` shows each value's `SOURCE` (`default` / `file` / `env`) so you can
see *why* a setting has its current value. The same effective config (values and
sources) is on `GET /health`, so the running daemon's configuration is always
observable.

## CLI

```sh
sherlog config list                 # every key, its effective value, and source
sherlog config get flood_keep       # one value (no decoration, scriptable)
sherlog config set flood_keep 50    # validate and persist one value
```

- `config list` prints a `KEY  VALUE  SOURCE` table over every key.
- `config get <key>` prints just the effective value, so it is safe to use in a
  script.
- `config set <key> <value>` validates the value and writes it atomically to
  `~/.sherlog/config.json` (temp file + rename), preserving the other keys. It
  confirms the write rather than asserting the effective source — an env override
  like `SHERLOG_PORT` can still win over the file, so `config list` remains the
  place to read effective sources.

Editing `config.json` directly is equivalent; the file holds only the keys you set
(absent keys take their defaults).

## When changes apply

Configuration is read **once at daemon start** — there is no hot reload. After
changing a value, restart the daemon (or stop it and let the next tool call
re-spawn it) to pick up the change.

## Retention deletes data

> With `retention_days > 0`, closed sessions older than the window are deleted
> from `~/.sherlog` (disk and memory) at startup and every 24 hours; each prune
> logs the session IDs it removed. **Open sessions are never pruned.** If you value
> the archive of past solved cases (and case recall, which draws on them), leave
> `retention_days` at its default of `0`.

## Example `config.json`

```json
{
  "flood_keep": 50,
  "await_debounce_seconds": 3,
  "verbosity": "minimal"
}
```

See [architecture.md](architecture.md) for how flood control, await/debounce, and
retention behave, and [troubleshooting.md](troubleshooting.md) for port conflicts.
