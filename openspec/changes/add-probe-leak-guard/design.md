# Design: add-probe-leak-guard

## Context

Every probe URL has one stable shape (docs/probe-contract.md): `http://127.0.0.1:<port>/log/<session>/<probe>`, where `<session>` is an 8-char crypto-random base36 ID (`internal/store/id.go`) and also the name of the session's directory under `~/.sherlog/sessions/`. `debug_end` already returns `…/log/<session>/` as the `greppable_fragment` for the cleanup grep gate — but that gate only runs on a proper close. `sherlog probes --stale` proves the precedent for CLI commands that read the store with no daemon running (D10). Retention (`PruneClosedBefore`) deletes old closed sessions, so the on-disk session set is not the complete set of IDs that ever existed.

The plugin root is the repository root (`.claude-plugin/plugin.json`, `skills/`), so a `hooks/hooks.json` there ships with the plugin and needs no installer.

## Goals / Non-Goals

**Goals:**

- A probe-carrying commit or push is refused at two independent layers, with no daemon, no network, and no way for a guard bug to wedge git.
- Zero false positives on documentation and examples (which use `<session>` placeholders).
- `sherlog doctor` turns the troubleshooting guide's diagnostics into one read-only command.
- Every gate — existing store gates and the new guard — has a recorded attack asserting its refusal, and a new gate cannot ship without one.

**Non-Goals:**

- An escape hatch / `SHERLOG_GUARD=off`. An agent could set it as easily as a user; the legitimate repair (remove the probe, close the case) is always available. A user who truly needs to commit a probe URL commits from their terminal without the opt-in git hooks.
- Scanning untracked files, stashes, or non-git VCSs.
- Hooks for other harnesses (Codex, Gemini, Cursor). The detector is harness-neutral; adapters can come later.
- Auto-installing the git hooks. Writing into a user's `.git/hooks` stays an explicit act.
- Doctor auto-repair (`--fix`). Read-only first; repair is a later change if field data asks for it.

## Decisions

### D-A · Two match tiers: known IDs, then loopback shape

Tier 1 matches `/log/<id>/` against the set of directory names under `<root>/sessions` — exact, host-agnostic (catches `localhost`, custom ports, proxied URLs), and costs one `os.ReadDir`, no JSON parse, no log replay. Tier 2 catches what tier 1 can't see — sessions pruned by retention, or probes from another machine — with the regex `(127\.0\.0\.1|localhost):\d+/log/[0-9a-z]{8,}/`. Findings carry their tier, and tier-1 findings carry the session's open/closed status (read from `state.json` only *after* a match, so the hot path never parses state).

*Alternative considered:* match any `:2218/log/` substring. Rejected — it false-positives on every doc, example, and test fixture in sherlog's own repo and in any repo that vendors the probe contract, and misses `SHERLOG_PORT` overrides. *Alternative considered:* match only sessions with unremoved probes. Rejected — a probe line left behind by a case closed "clean" by mistake is exactly the leak to catch; the session's existence is the signal.

### D-B · Added lines only, from git's own diff

Detection runs over unified diff text (`git diff -U0 --no-color --no-ext-diff`) and inspects only lines starting with `+` (excluding the `+++` header). Removing a probe is never a finding, and pre-existing lines outside the change are never re-litigated. `--no-ext-diff` and `--no-color` defend against user diff config altering the format; `-c core.quotePath=false` keeps file names readable in findings.

| Operation | Diff scanned |
|---|---|
| hook, `git commit` | `git diff --cached` |
| hook, `git commit` with `-a/--all/-i/--include/-o/--only/pathspec` | also `git diff HEAD` (tracked worktree vs HEAD) |
| hook, `git push` | `git log -p --format=%H <upstream>..HEAD`, or `HEAD --not --remotes` with no upstream |
| git `pre-commit` | `git diff --cached` (git has already staged `-a` content by now — exact) |
| git `pre-push` | per stdin line `<lref> <lsha> <rref> <rsha>`: `rsha..lsha`, or `lsha --not --remotes` when `rsha` is all zeros; deletions skipped |

### D-C · Command detection is deliberately over-inclusive

The harness hook sees a command *string*, not a parsed git invocation. It tokenizes on shell separators (`;`, `&&`, `||`, `|`, newlines) and flags any segment whose first non-assignment word ends in `git` (covers `/usr/bin/git`, `GIT_DIR=x git`) and contains a `commit` or `push` token after skipping git's global options (`-C <dir>`, `-c <k=v>`, `--git-dir=…`). Over-triggering costs one clean scan and an allow; under-triggering is backstopped by the git layer. The repository scanned is the payload `cwd`, adjusted by a `-C <dir>` or a leading `cd <dir> &&` when present. Indirection (an alias, a script, a Makefile target) is a documented residual gap — the git layer is what covers it, the same split safedose-rails uses.

### D-D · Fail open, everywhere, by construction

The hook entry point recovers panics and maps *every* error path to "allow" (exit 0, no output); only a non-empty finding list produces a deny. Errors are not silent to the user: they are reported by `sherlog doctor` (guard row runs a self-test scan) rather than on the hot path. *Rationale:* a guard that can break `git commit` for an unrelated reason gets uninstalled within a day, and an uninstalled guard protects nothing. This mirrors safedose-rails' "the gate reads local files only — nothing that can hang; every failure path exits 0."

### D-E · Hook protocol

`hooks/hooks.json` registers `PreToolUse`, matcher `Bash`, command `sherlog guard hook`. Deny is signaled with the documented structured output (`hookSpecificOutput.permissionDecision: "deny"` + reason) and exit 0; the reason text is what the agent sees. The exact JSON shape is verified against current Claude Code hook docs during implementation (task 2.1) and pinned by a corpus case so a format drift fails a test, not production. If `sherlog` is not on PATH the hook command fails to start, which Claude Code treats as a non-blocking hook error — fail-open again, and doctor's PATH row is the diagnostic.

### D-F · Git hook shims are tiny and owned

`sherlog guard install` resolves the hooks directory with `git rev-parse --git-path hooks` (respects `core.hooksPath` and worktrees) and writes two POSIX-sh shims containing a `# sherlog-guard v1` marker line and `exec sherlog guard git <hook> "$@"`. A hook file without the marker is foreign: never overwritten; install exits 1 and prints the one line to append. `sherlog guard uninstall` removes only marked files. Shims are fail-open too: `command -v sherlog >/dev/null || exit 0`. On Windows, Git for Windows runs hooks through its bundled sh, so the same shim works.

### D-G · Package layout

- `internal/guard` — `Detect(root string, diff io.Reader) ([]Finding, error)`, the diff extractors (`StagedDiff`, `WorktreeDiff`, `PushDiff`, `PrePushDiff`) behind a small `git` runner interface (fakeable in tests), the Claude hook adapter (`HandleClaudeHook(stdin, stdout)`), command detection, and shim install/uninstall.
- `internal/doctor` — `Check` = `{Name, Run func(Env) Row}`; `Env` injects root, PATH lookup, executable path, HTTP getter, and git runner so every check is unit-testable without the real machine. Renderer is a `tabwriter` like `probes --stale`.
- `cmd/sherlog/guard.go`, `cmd/sherlog/doctor.go` — stdlib `flag` dispatch only.

### D-H · The bypass corpus

Two corpora, one rule: **every refusal path has an attack recorded against it.**

- **Store gates** — `internal/store/bypass_test.go`: a table of `{name, attack (prose), setup func, try func, want error}`. Seed attacks include confirm without citation, citation to an open run, citation to a fabricated probe ID, confirm before any reproduced run, confirm citing an unpredicted probe, out-of-enum and empty status, fixed-check close without prediction, solved close naming an active hypothesis, partial resolution, blast-radius annotation of a site not in the hits, and annotation with an invalid verdict. A meta-test reflects over the exported `Err*` gate sentinels in `gates.go` and `blast_radius.go` and fails if any sentinel has no corpus entry — a new gate cannot merge un-attacked. Lookup sentinels (`ErrSessionNotFound`, etc.) are excluded by an explicit allowlist, so excluding a gate is a visible, reviewed act.
- **Guard** — `internal/guard/testdata/corpus/*.json`: `{name, attack, payload (hook JSON), repo: {staged, worktree, commits}, sessions: [...], want: allow|deny}`. A table-driven test builds a temp git repo per case. Seed attacks: absolute-path git, `git -C`, `cd x &&`, `--no-verify`, `-c core.hooksPath=/dev/null`, `commit -am` with an unstaged probe, pathspec commit, probe on `localhost` with custom port, pruned-session probe (shape tier), probe split across a CRLF file, push of a probe committed outside the session; plus must-allow cases: doc placeholders, probe removal, unrelated Bash, malformed JSON, missing root.

## Risks / Trade-offs

- **[Command-string detection misses indirection]** → git layer backstops; documented as a residual gap in `docs/guard.md`, as safedose-rails does for the same limitation.
- **[Git layer is opt-in, so terminal commits are unguarded by default]** → skill offers `guard install` at close; doctor shows `--`/install hint per repo. Auto-install rejected (writing into `.git/hooks` without consent).
- **[Tier-2 shape false positive]** → a real base36 token after `localhost:<port>/log/` in committed code is vanishingly unlikely to be anything but a probe; if it is, the user commits from the terminal. Revisit only with field evidence.
- **[Hook latency on every Bash call]** → non-git commands return after string tokenization only; git commands pay one `ReadDir` + one `git diff`. A benchmark pins the non-git path; target < 5 ms.
- **[Large pushes]** → `git log -p` over a long unpushed range can be big; the detector streams line by line and never buffers the whole diff.
- **[Hook JSON format drift in Claude Code]** → pinned by a corpus case; a drift fails CI rather than silently fail-opening forever.
