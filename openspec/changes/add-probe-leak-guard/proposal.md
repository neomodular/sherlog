# Proposal: add-probe-leak-guard

## Why

The worst outcome sherlog can produce is a probe — a live `POST` to `127.0.0.1:2218/log/<session>/<probe>` — shipped in a commit. Today the only defense is the grep gate inside `debug_end`, which runs *only if the case is closed properly*: an agent that commits mid-investigation, a session that crashes, or a case abandoned for days all sail past it, and nothing at the git or harness layer notices. Measured against the Stage-4 workflow bar (guardrails *between* steps, punch-outs that are *actively bypass-tested*), sherlog's daemon gates are strong but sit on one layer only, and they have never been attacked systematically — the out-of-enum status bypass (`ErrInvalidHypothesisStatus`) was found by a dogfood run by luck, not by method. Install health is likewise diagnosed by reading `docs/troubleshooting.md` by hand.

## What Changes

- **Probe-leak guard, two layers.** A new `sherlog guard` subcommand refuses a commit or push whose *added lines* contain a sherlog probe URL:
  - **Harness layer** — the plugin ships a `PreToolUse` hook (`hooks/hooks.json`, matcher `Bash`) that runs `sherlog guard hook` on every Bash call; when the command looks like a `git commit`/`git push`, it scans what would be committed/pushed and denies with an actionable message (remove the probe, or finish the case with `debug_end`). Ships with the plugin — no installer step.
  - **Git layer (opt-in)** — `sherlog guard install` writes `pre-commit` and `pre-push` shims into the current repository (never overwriting a foreign hook), catching commits that reach git by any route, including the user's own terminal.
  - Detection keys on the probe URL shape (`/log/<session-id>/`) matched against the session IDs on disk, plus a loopback-shape fallback for sessions already pruned by retention. Documentation placeholders (`<session>`) never match.
  - Reads `~/.sherlog` directly (a directory listing — no daemon, no log replay, no network) and **fails open**: any internal error allows the operation, so the guard can never wedge a developer's git.
- **`sherlog doctor`.** A read-only health check printing one aligned `ok` / `!!` / `--` row per check — binary version and PATH resolution, storage root writable, session state readable, config valid, daemon reachable / same version / owns the port, probes left behind by closed cases, guard installed in the current repo — exiting 1 when any row is `!!`.
- **Gate bypass corpus.** A table of named attacks against every store gate sentinel and every guard detection path, each asserting the exact refusal. A meta-test fails when a gate sentinel exists with no attack recorded against it, so a new gate cannot ship un-attacked.
- **Skill update.** `/debug` learns that a refused commit is the leak guard doing its job: remove the probes (or close the case) — never bypass, never `--no-verify`.

## Capabilities

### New Capabilities

- `probe-leak-guard`: detection of sherlog probe URLs in content about to be committed or pushed, the harness and git enforcement layers, their install path, and fail-open behavior.
- `install-doctor`: the read-only `sherlog doctor` health report — the checks, row format, and exit status.

### Modified Capabilities

- `debug-skill`: ADDED — how the skill responds to a leak-guard refusal (repair, never bypass) and when it suggests `sherlog guard install`.

The bypass corpus is a test asset, not system behavior, so it carries no spec; it lives in design and tasks.

## Impact

- **Code:** new `internal/guard` (detector, git diff extraction, hook payload adapter, shim install), new `internal/doctor` (checks + renderer), `cmd/sherlog` (`guard`, `doctor` dispatch — stdlib `flag`, no CLI framework), `internal/store/bypass_test.go` + `internal/guard/testdata/` corpus, new `hooks/hooks.json` at the plugin root, `skills/debug/SKILL.md`.
- **Docs (same PR, per convention):** new `docs/guard.md`; `docs/troubleshooting.md` gains `sherlog doctor` as the first step; README command list; `docs/architecture.md` notes the guard as the second enforcement layer.
- **Dependencies:** none — stdlib only; `git` is invoked as a subprocess and its absence fails open.
- **Security invariants:** unchanged. The guard is local, read-only against `~/.sherlog`, makes no network calls, and uploads nothing; `doctor` performs one loopback `GET /health`.
- **Behavior:** Claude Code sessions with the plugin installed will have probe-carrying commits refused where they previously succeeded — that is the point. Commits without probes are unaffected beyond one directory listing and one `git diff` per commit/push command.
