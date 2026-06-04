# Dogfood results

Two seeded example apps exercise the `/debug` loop:

- [`node-app/`](node-app/) — an intermittent `401 "no token"` caused by a token-
  refresh race (a null window in the cache).
- [`browser-page/`](browser-page/) — a blank profile name caused by reading a
  fetch body as text and then accessing `.name` on the string.

Each README walks the detective loop you run live inside Claude Code (suspects →
discriminating probes → reproduce → evidence → fix → fixed-check → cleanup).

## Scripted end-to-end simulation (automated, against the real daemon)

Live `/debug` dogfooding inside Claude Code is reserved for the user (task 6.4
stays unchecked). To validate the wiring the skill depends on without a human in
the loop, a scripted simulation starts the **real `sherlog daemon` binary** on a
test port and drives the full investigation lifecycle over the internal `/api/`
surface, firing probe events that mimic the node-app scenario.

What the simulation exercises and asserts:

| Step | Tool / endpoint | Result |
|---|---|---|
| Daemon health | `GET /health` | 200, `{version, uptime}` |
| `debug_start` | `POST /api/sessions` | session id `ltyvmfoo`, `existing_same_cwd: null` |
| `set_hypotheses` (3 suspects) | `PUT …/hypotheses` | `h1, h2, h3` all `active` |
| `register_probe` ×3 (one per suspect) | `POST …/probes` | `p1→h1`, `p2→h2`, `p3→h3`, each with file+line |
| Probe ingest (browser/node-style) | `POST /log/<sess>/<probe>` | 200 on every hit; **no JSON content-type**; `Access-Control-Allow-Origin: *` asserted present |
| `await_run` #1 (repro) | `POST …/await` | returned `reason: "quiet"` via the ~2s debounce after p1 fired 6× |
| `close_run` | `POST …/runs/close` | verdict `reproduced` recorded, run `r1` closed |
| `query_logs probe=p1` | `GET …/query?probe=p1` | `total: 6`, events show `cachedToken:null, refreshing:true` — the race signature |
| `query_logs probe=p3` (never fired) | `GET …/query?probe=p3` | `total: 0` (distinguishable from "no data", not an empty array) |
| `update_hypothesis` ×3 | `PATCH …/hypotheses/<id>` | `h1` confirmed, `h2`/`h3` killed, each with an evidence note |
| `await_run` #2 (fixed-check) | `POST …/await` | `reason: "quiet"`; p1 now shows `cachedToken:"tok-ok", refreshing:false`; p2/p3 `total:0` |
| `close_run` | `POST …/runs/close` | verdict `fixed-check`, run `r2` closed |
| `debug_end` (p1,p2 removed; p3 left) | `DELETE …/probes/<id>`, `DELETE …/sessions/<id>` | `unremoved_probes` correctly lists only `p3` (file `server.js`, line 50) |
| `debug_resume` by id | `GET …/sessions/<id>` | full board, probe registry (`p1`/`p2` `removed:true`, `p3` false), and both runs with verdicts replayed from daemon state |

**Outcome: `SIMULATION OK` (exit 0).** Every lifecycle step behaved as the skill
assumes:

- The await engine opens a run, debounces on quiet (~2s after first activity),
  and returns a per-probe summary rather than raw logs.
- A registered probe that never fires is reported with `total: 0` in both the run
  summary and `query_logs` — the signal the skill uses to kill a hypothesis.
- The discriminating evidence flips between runs exactly as a fix verification
  expects: `cachedToken:null/refreshing:true` (bug) → `cachedToken:"tok-ok"/
  refreshing:false` (fixed).
- `debug_end` enumerates exactly the probes not yet marked removed — the data
  behind the cleanup gate.
- Investigation state survives and is reconstructable via the resume path.

### Reproducing the simulation

The simulation is a standalone script run against a built binary, with an
isolated `HOME`/`USERPROFILE` so it never touches `~/.sherlog`, and
`SHERLOG_PORT` set to a free test port:

```
go build -o /tmp/sherlog ./cmd/sherlog
HOME=<tmp> SHERLOG_BIN=/tmp/sherlog SHERLOG_PORT=2299 go run sim.go
```

(`sim.go` lives outside the module tree during testing so it stays out of
`go build ./...`/`go test ./...`; its logic mirrors the table above.)

## Live dogfood (reserved for the user — task 6.4)

Run `/debug` inside Claude Code against each example, following its README, and
confirm: ≥3 suspects on the board, one discriminating probe per suspect, the
"the game is afoot" blocking wait, evidence-based kills, the fix, the
`fixed-check` run, "elementary.", and a grep-verified "case closed".
