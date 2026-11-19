# Proposal: add-witnessed-verdicts

## Why

The user's reproduction and their verdict on it are sherlog's human gate: every kill, confirm, and fixed-check is judged against a run whose verdict is, per the `log-query` spec, "user-supplied". In practice the **agent** supplies it — `close_run(verdict=…)` carries whatever the agent says the user said, and nothing distinguishes a verdict the user gave from one the agent assumed. The probe evidence is unforgeable (live ingest); the verdict framing it is on the honor system. The sharpest case is `fixed-check`: an agent eager to finish can close the retest itself and the case is counted solved. safedose-rails hit the same gap — an agent typed `sdrails approve` — and closed it with an opt-in strict profile that refuses the human verb from inside an agent session, twice (by command string and by process environment), and records every approval as witnessed.

## What Changes

- **Verdict provenance on every run** — each closed run records `verdict_source`: `agent` (via the `close_run` tool) or `witness` (via the new terminal command). Recorded in all modes; additive on disk.
- **`sherlog verdict`** — a terminal command for the user: with no argument it shows the open run of the current case (title, per-probe event counts, and for a fix retest the recorded prediction) and prompts for the verdict; `sherlog verdict <reproduced|not-reproduced|fixed-check>` answers directly. It refuses to run from an agent session — when `CLAUDECODE` is set or stdin is not a terminal — so only a person at their own terminal can witness. It talks to the daemon (which owns state) and never writes state files itself.
- **Witnessed mode** — new config key `verdicts` (`agent` default | `witnessed`). Under `witnessed`:
  - `close_run` from the agent is refused with an actionable message telling the user the exact command to run;
  - the harness hook refuses an agent Bash call that invokes `sherlog verdict`, a second layer beside the CLI's own environment check;
  - case-next puts the command in `awaiting_human` and no longer lists `close_run` as ready;
  - switching the mode back to `agent` is a config change the user makes — no tool exposes it.
- **Stats and audit** — `run_closed` audit records carry the source; stats report the witnessed share of verdicts and of passing fix checks, so the verified solve rate can be read with its provenance.
- **Skill** — under `witnessed`, the skill asks the user to run `sherlog verdict` in their own terminal (not via `!`, which runs inside the agent session) and continues from `next` once the verdict is recorded.

Honest limit, stated in the docs: the agent runs as the same OS user, so a deliberate forgery (crafting the daemon request by hand) cannot be prevented locally — the same residual gap rails documents. Witnessed mode turns the *casual* path — an agent filling in a verdict argument without noticing — into a refusal, and makes any forgery a deliberate act.

## Capabilities

### New Capabilities

- `witnessed-verdicts`: verdict provenance, the `sherlog verdict` terminal command and its agent-session refusal, witnessed mode's enforcement at the tool and harness layers, and the user-only mode switch.

### Modified Capabilities

- `log-query`: MODIFIED — "Run verdicts": closing records the verdict source, and under witnessed mode the agent's `close_run` is refused.
- `configuration`: ADDED — the `verdicts` key.
- `debug-skill`: ADDED — witnessed-mode interaction (where the user types the verdict, why not `!`).
- `case-next`: ADDED — witnessed mode's human wait names the command; `close_run` is not ready.
- `case-audit`: ADDED — `run_closed` records carry the verdict source; witnessed-mode refusals use a gate code.
- `case-stats`: ADDED — witnessed share of verdicts and of passing fix checks.

## Impact

- **Code:** `internal/store` (`Run.VerdictSource`, refusal sentinel for an unwitnessed close), daemon close endpoint accepts a source, `internal/mcp` (`close_run` passes `agent`), `cmd/sherlog/verdict.go`, `internal/config/keys.go`, `internal/guard` hook command detection, `internal/store/next.go`, audit + stats emission, `skills/debug/SKILL.md`, Case Board run timeline shows the source.
- **Docs (same PR):** `docs/tools-reference.md` (`close_run` refusal in witnessed mode), `docs/configuration.md` (`verdicts`), new section in `docs/guard.md` or a `docs/witnessed-verdicts.md`, README commands, `CLAUDE.md`.
- **Depends on:** `add-case-next`, `add-probe-leak-guard` (hook entry point), `add-case-audit-and-stats` — lands last in the roadmap.
- **Compatibility:** default mode `agent` keeps today's behavior exactly, plus the recorded source; pre-change runs load with no source and display as `unknown`.
- **Security invariants:** unchanged — the Case Board stays GET-only (no verdict button: that would break the read-only invariant), loopback only, nothing uploaded.
