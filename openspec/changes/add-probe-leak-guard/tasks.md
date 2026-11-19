# Tasks: add-probe-leak-guard

## 1. Guard core — detection (no git, no hooks yet)

- [ ] 1.1 `internal/guard` package skeleton: `Finding{File, Line, SessionID, Tier, SessionState}` and `Detect(root, io.Reader)` streaming unified-diff text line by line, inspecting only `+` lines (not `+++` headers), tracking the current file and new-side line numbers from `@@` hunks; table-driven tests over diff fixtures (added probe, removed probe, context line, multiple hunks, CRLF, rename header)
- [ ] 1.2 Tier 1: known-session set from `os.ReadDir(<root>/sessions)` (dirs only); `/log/<id>/` match, host/port-agnostic; session open/closed resolved from `state.json` only after a match; missing root yields an empty set, not an error; tests use an injectable temp root (never the real `~/.sherlog`)
- [ ] 1.3 Tier 2: loopback shape regex `(127\.0\.0\.1|localhost):\d+/log/[0-9a-z]{8,}/` for IDs not on disk; placeholder `/log/<session>/` never matches; tests for pruned-session match, placeholder, uppercase/short-token non-match
- [ ] 1.4 Refusal message renderer: one line per finding (file:line, session, tier, open/closed/unknown) plus the repair sentence (remove the lines, or finish the case via `debug_end` cleanup); never mentions disabling the guard; golden-string test

## 2. Guard — harness layer (Claude Code PreToolUse)

- [ ] 2.1 Verify the current Claude Code `PreToolUse` hook input/output contract (stdin payload fields `tool_name`, `tool_input.command`, `cwd`; structured deny via `hookSpecificOutput.permissionDecision`) against the official hooks docs; record the doc reference in a comment on the adapter; verified by a corpus case pinning the exact deny JSON
- [ ] 2.2 Command detection (D-C): segment on `;`, `&&`, `||`, `|`, newlines; skip env assignments; first word ends in `git`; skip `-C <dir>`, `-c k=v`, `--git-dir=…`; detect `commit`/`push`; extract `-C` / leading `cd <dir> &&` as the scan directory; detect working-tree commit forms (`-a`, `--all`, `-am`, `-i`, `--include`, `-o`, `--only`, pathspec); table-driven tests including `/usr/bin/git`, `GIT_DIR=x git commit`, `echo git commit` (no trigger), `git log --grep=commit` (no trigger)
- [ ] 2.3 Git diff extractors behind a fakeable runner interface: `StagedDiff`, `WorktreeDiff` (vs HEAD), `PushDiff` (upstream range, else `HEAD --not --remotes`), all with `--no-color --no-ext-diff -U0 -c core.quotePath=false`; tests against real temp git repos (skip cleanly when `git` is absent)
- [ ] 2.4 `HandleClaudeHook(stdin, stdout)`: non-Bash or non-git → allow with no repo access; git commit/push → extract + detect → deny JSON with the rendered message, else allow; every error and panic → allow (D-D); tests for malformed JSON, missing cwd, non-repo cwd, panic recovery
- [ ] 2.5 `cmd/sherlog/guard.go`: `sherlog guard hook` dispatch (stdlib `flag`) + `usage` entry; `hooks/hooks.json` at the plugin root registering `PreToolUse`/`Bash` → `sherlog guard hook`; verified by `claude --plugin-dir .` manual check: a staged probe commit is denied with the message, `go test` runs untouched
- [ ] 2.6 Hot-path benchmark: non-git command decision path; assert under 5 ms in a test guard (`testing.Benchmark`-based threshold, generous on CI)

## 3. Guard — git layer (opt-in)

- [ ] 3.1 `sherlog guard git pre-commit` (staged diff) and `sherlog guard git pre-push` (stdin ranges per D-B: `rsha..lsha`, zero `rsha` → `lsha --not --remotes`, branch deletions skipped); exit 1 with the rendered message on findings, 0 otherwise and on any error; tests with real temp repos including a new-branch push and a deletion
- [ ] 3.2 `sherlog guard install` / `uninstall`: hooks dir via `git rev-parse --git-path hooks` (honors `core.hooksPath`, worktrees); POSIX-sh shims with `# sherlog-guard v1` marker, `command -v sherlog || exit 0`, `exec sherlog guard git <hook> "$@"`; foreign hook never overwritten (exit 1 + chain line printed); idempotent re-install; uninstall removes only marked files; mode 0755; tests for each case
- [ ] 3.3 End-to-end: temp repo + installed shims + temp root with a session dir; `git commit` of a probe line refused, removal commit allowed, `git push` to a local bare remote refused for a probe commit (PATH points at the freshly built test binary)

## 4. Bypass corpus (Stage-4 criterion 3)

- [ ] 4.1 `internal/store/bypass_test.go`: attack table `{name, attack, setup, try, want}` seeded with every attack listed in design D-H; each asserts `errors.Is` on the exact sentinel and that the board is unchanged after the refusal
- [ ] 4.2 Coverage meta-test: parse `gates.go` and `blast_radius.go` (`go/parser`) for exported `Err*` sentinels; fail naming any sentinel with no corpus entry; explicit reviewed allowlist for lookup sentinels (`ErrSessionNotFound`, `ErrNoOpenSession`, `ErrRunNotFound`, `ErrNoOpenRun`, `ErrHypothesisNotFound`, `ErrProbeNotFound`)
- [ ] 4.3 Guard corpus `internal/guard/testdata/corpus/*.json` (`{name, attack, payload, repo, sessions, want}`) seeded with every attack and must-allow case in D-H; table-driven runner builds a temp repo per case; a case that flips verdict fails with its `attack` text
- [ ] 4.4 Any attack in 4.1–4.3 that is NOT refused today is a real bypass: fix it in this change with its own regression case and note it in the PR description (do not weaken the corpus to pass)

## 5. Doctor

- [ ] 5.1 `internal/doctor`: `Check`/`Row`/`Env` types (injected root, PATH lookup, executable path, HTTP getter, git runner); renderer with aligned `ok`/`!!`/`--` columns; exit-status helper (1 iff any `!!`); tests for rendering and exit status
- [ ] 5.2 Checks: version; PATH resolution vs running executable (symlink-resolved); storage root exists-or-creatable and writable (probe via `os.CreateTemp` + remove — the one write, cleaned up; document it); every session `state.json` parses (names corrupt IDs, keeps going); config `Load` + validate; tests per check with fakes
- [ ] 5.3 Daemon check via loopback `GET /health` on the effective port, never spawning: down → `--`; same version → `ok`; different version → `--` "will restart on its own"; non-sherlog answer → `!!` pointing at `SHERLOG_PORT`; tests over `httptest` (never assume 2218 free)
- [ ] 5.4 Probes check: unremoved probes in closed sessions → `!!` with count + `sherlog probes --stale`; open-session probes → `--` informational; guard check: not a git repo → `--`, shims present with marker → `ok`, absent → `--` with `sherlog guard install` hint, plus a self-test scan of a synthetic diff proving the detector works (`!!` if it errors)
- [ ] 5.5 `cmd/sherlog/doctor.go` dispatch + `usage`; manual run on this machine shows every row; `go test ./cmd/sherlog/` covers dispatch and exit code

## 6. Skill + docs (same PR, docs-match-binary convention)

- [ ] 6.1 `skills/debug/SKILL.md`: leak-guard refusal handling (repair: unstage/remove probes or finish the case; never `--no-verify`, never edit hook config, never mutate the probe URL, never `report_observation` a refusal) + one-line `sherlog guard install` offer at close when the repo lacks it; checklist entries updated
- [ ] 6.2 New `docs/guard.md`: what is detected (tiers), both layers, install/uninstall, fail-open behavior, the no-escape-hatch decision, residual gaps (command-string indirection, opt-in git layer, untracked files); link from README
- [ ] 6.3 `docs/troubleshooting.md`: `sherlog doctor` as step one, each `!!` row mapped to its section; README command list gains `guard` and `doctor`; `docs/architecture.md` notes the guard as the second enforcement layer beside the daemon gates; `CLAUDE.md` binary-commands block updated

## 7. Validation

- [ ] 7.1 Full local suite clean: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (gofmt prints nothing), plus `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] 7.2 Dogfood: `go install ./cmd/sherlog`, run a real `/debug` case on `examples/node-app`, attempt to commit mid-case → refused by the hook; `sherlog guard install` → terminal commit refused; close the case → commit allowed; `sherlog doctor` all `ok`/`--`
- [ ] 7.3 `openspec validate add-probe-leak-guard --strict` passes

## Workflow follow-up

- Archive with `/opsx:archive` after merge.
- Next change in the Stage-4 roadmap: `add-case-audit-and-stats` (criteria 4 and 5 — audit trail of transitions and rejections, `sherlog stats` with printed definitions).
