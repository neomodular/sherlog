# Tasks: add-witnessed-verdicts

> Prerequisites: `add-probe-leak-guard`, `add-case-next`, `add-case-audit-and-stats` merged and archived (this change's deltas target `case-next`, `case-audit`, `case-stats`). Lands last in the roadmap.

## 1. Store + config

- [ ] 1.1 `verdicts` key in `internal/config/keys.go` (string enum `agent`|`witnessed`, default `agent`, set/validate, listed with source); config tests for list/get/set and invalid values; `docs/configuration.md` row marking it live (D-C)
- [ ] 1.2 Live accessor: `config.Load` cached on config-file mtime+size; tests that a `config set` is observed on the next read without a daemon restart and that an unchanged file is not re-parsed
- [ ] 1.3 `Run.VerdictSource` (`agent`|`witness`, empty ⇒ `unknown` at read time); close path records it; `ErrVerdictNotWitnessed` refusal in witnessed mode for source `agent`, run left open; gate code `verdict-not-witnessed`; table tests; new attack in the bypass corpus (agent close under witnessed mode); legacy state fixture loads with `unknown`

## 2. Daemon + MCP + CLI

- [ ] 2.1 Close endpoint accepts `source` in the body (absent ⇒ `agent`); refusal mapped to the gate 4xx with the actionable message naming `sherlog verdict`; `httptest` tests for both modes (never assume port 2218)
- [ ] 2.2 `cmd/sherlog/verdict.go`: refusal when `CLAUDECODE` is set or stdin is not a char device (message states which check fired and calls out `!`); interactive prompt showing title, run, per-probe counts, recorded prediction; argument form; `--session`; non-zero exit with no change when no run is open; tests with injected env/stdin-mode/daemon client; verify char-device detection on a Windows console (or document the `CLAUDECODE`-only fallback)
- [ ] 2.3 MCP `close_run` sends `source: agent`; tool description notes the witnessed-mode refusal; `docs/tools-reference.md` updated

## 3. Harness layer

- [ ] 3.1 `sherlog guard hook` second detector (D-E): in witnessed mode deny Bash segments invoking `sherlog verdict` or `sherlog config set verdicts` (absolute paths, env prefixes, `&&` chains); allow in agent mode; fail-open; guard corpus cases for each, including the `config set` attempt

## 4. Evaluator, audit, stats

- [ ] 4.1 `Evaluate(sess, Options{Verdicts})` (D-C2); witnessed mode: open run → `awaiting_human` names `sherlog verdict`, `close_run` absent from `ready`; table tests for both modes; callers pass the live mode
- [ ] 4.2 `run_closed` audit records carry `source`; backfill writes `unknown`; refusal recorded with `verdict-not-witnessed`; tests
- [ ] 4.3 Stats: source shares of closed runs and witnessed passing fix checks among `solved`; definitions text updated; golden tests

## 5. Skill, board, docs

- [ ] 5.1 `skills/debug/SKILL.md`: witnessed-mode flow (ask for the terminal command, explain `!`, never call `close_run` or the command, continue from `debug_resume`/`next`); checklist updated
- [ ] 5.2 Case Board run timeline shows each run's verdict source (`witness`/`agent`/`unknown`); GET-only unchanged
- [ ] 5.3 Docs: `docs/witnessed-verdicts.md` (what it does, how to turn it on, the two discriminators, the same-user residual gap stated plainly, why no board button and no token); README and `CLAUDE.md` command lists

## 6. Validation

- [ ] 6.1 Full local suite clean: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (gofmt prints nothing), plus `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] 6.2 Dogfood with `verdicts=witnessed` set while the daemon is running (proves live read): a full `/debug` case where the agent's `close_run` is refused, `! sherlog verdict` is refused, the hook denies the agent's `sherlog verdict` and `config set verdicts agent`, the user closes each run from a separate terminal after seeing counts and the prediction; `sherlog audit` and `sherlog stats` show `witness` sources
- [ ] 6.3 `openspec validate add-witnessed-verdicts --strict` passes

## Workflow follow-up

- Archive after the three prerequisite changes are archived.
- Then the docs-only PR: `WORKFLOW.md` + `certification/README.md` mapping the five Stage-4 criteria to their artifacts, with witnessed mode as the strengthened punch-out for criterion 3.
