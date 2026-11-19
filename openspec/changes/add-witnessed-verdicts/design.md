# Design: add-witnessed-verdicts

## Context

Runs close through one daemon endpoint, `POST /api/sessions/<id>/runs/close` (`internal/daemon/server.go`), reached only by the MCP `close_run` tool today, with the verdict enum checked client-side (`validVerdict`). Config is loaded once at daemon start (`internal/daemon/daemon.go`) and `docs/configuration.md` tells users to restart after a change. In a Claude Code Bash tool process, `CLAUDECODE` is set and stdin is not a terminal (verified in-session); the user's own terminal has a TTY and no `CLAUDECODE`. The `!` prefix in Claude Code runs inside the session environment. The leak guard's `sherlog guard hook` is the existing PreToolUse entry point for Bash command inspection. safedose-rails' strict profile (POLICY.md §1) is the precedent: the human verb refused to agents by command string at the hook and by process environment in the CLI, and every approval recorded as witnessed.

## Goals / Non-Goals

**Goals:**

- Every verdict carries recorded provenance, in every mode.
- An opt-in mode where the casual path — the agent filling in a verdict — is refused at two independent layers.
- The person giving the verdict sees the evidence and the prediction they are judging.

**Non-Goals:**

- Preventing a deliberate same-user forgery. The agent can read sherlog's source and craft the daemon request; no local mechanism separates two processes of one OS user. Documented as the residual gap, exactly as rails documents `sdrails off` and `core.hooksPath`.
- A verdict button on the Case Board. The board is GET-only by security invariant; a mutation endpoint for browsers is the attack surface that invariant exists to deny.
- A shared secret or token file. Anything the CLI can read, the agent can read; it would be ceremony that implies a guarantee it cannot give.
- Witnessing anything other than verdicts (hypothesis transitions stay agent judgments over witnessed evidence).

## Decisions

### D-A · Two discriminators, both cheap, both stdlib

`sherlog verdict` refuses when `os.Getenv("CLAUDECODE") != ""` **or** stdin is not a character device (`os.Stdin.Stat()` → `Mode()&os.ModeCharDevice == 0`, no `x/term` dependency). Either alone has a hole — an agent can unset an env var; a pipe-free harness could someday allocate a PTY — together they make the casual path fail and the deliberate path obvious. The refusal message names the user's own terminal and calls out `!` explicitly, because "I typed it in Claude Code" is the predictable mistake.

### D-B · Source travels in the close request; the gate lives in the store

The close endpoint body gains `source` (`agent` | `witness`; absent ⇒ `agent`, so older MCP processes stay correct). MCP always sends `agent`. The store's close path records `Run.VerdictSource` and, in witnessed mode, refuses `agent` with a new sentinel `ErrVerdictNotWitnessed` (gate code `verdict-not-witnessed`), so the refusal joins the bypass corpus, the gate-code meta-test, and the audit with no special casing. The daemon trusts the `source` field — that is the same-user limit made explicit, not an oversight.

### D-C · `verdicts` is read live

Unlike every other key, `verdicts` is re-read on each close and each `next` evaluation through a small accessor that caches `config.Load` keyed on the config file's mtime+size. *Rationale:* a security mode that silently does nothing until a daemon restart is worse than no mode — the user believes the gate is on. The other keys keep restart semantics; `docs/configuration.md` marks `verdicts` as live.

### D-C2 · `Evaluate` gains its one input

case-next's `Evaluate(sess)` becomes `Evaluate(sess, Options{Verdicts})`. Determinism is preserved (same session + same mode ⇒ same answer); the structural independence from gates is unaffected.

### D-D · The interactive prompt is the default path

`sherlog verdict` with no argument fetches the case from the daemon (`GET` session + run summary), prints title, run, per-probe counts, and the recorded prediction for a retest, then reads one line from stdin (`r`/`n`/`f` or the full word). Showing the prediction is the point for `fixed-check`: the user judges the agent's recorded claim against the evidence, which is what "witnessed" should mean. The argument form exists for speed once the user knows the loop.

### D-E · Harness layer reuses the guard hook

`sherlog guard hook` already tokenizes Bash commands for git; it gains a second detector: when the live `verdicts` mode is `witnessed`, deny segments invoking `sherlog verdict` or `sherlog config set verdicts`. In `agent` mode it does not deny either (the CLI's own D-A check still refuses `sherlog verdict` from the agent — it can never witness — but there is nothing to protect by denying it earlier). Fail-open as in the leak guard.

### D-F · Provenance in history

`run_closed` audit records gain `source`; backfill writes `unknown` for legacy runs. Stats add source shares and witnessed passing fix checks. `solved` keeps its existing definition — provenance is reported beside the rate rather than redefining it, so turning witnessed mode on does not retroactively move the historical number.

## Risks / Trade-offs

- **[Friction: a second terminal per run]** → opt-in; default mode unchanged; the prompt shows the evidence so the extra step carries value; the skill explains where to type.
- **[False refusals in exotic setups]** (a user running sherlog under a non-TTY wrapper, or a terminal multiplexer that sets `CLAUDECODE` because Claude Code started it) → the message states both conditions it checked so the user can see which fired; no override flag, since an override is the agent's bypass too.
- **[Live config read cost]** → closes and evaluations are rare relative to ingest; the mtime cache makes the steady state a `stat`.
- **[Windows TTY detection]** → `ModeCharDevice` is set for console handles; verified in task 2.2 on a Windows runner, falling back to the `CLAUDECODE` check alone where it cannot be determined (documented).
- **[Agent edits the config file directly]** → the same-user limit again; the audit shows a `verdicts` change only indirectly (sources flip to `agent`), and the docs say so. A config-change audit record is a candidate follow-up, not scope here.
