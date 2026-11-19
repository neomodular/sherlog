# Tasks: add-case-next

## 1. Store — the evaluator

- [ ] 1.1 `internal/store/next.go`: `Stage` typed constants, `Next`/`Action`/`HumanWait`/`Anomaly` types with spec JSON tags; lists always marshal as `[]`; marshal test asserts empty lists render `[]` and field names match the spec
- [ ] 1.2 `Evaluate(sess)` implementing the D-B precedence and the D-C judged rule; table-driven tests with one row per precedence line plus every `case-next` spec scenario (board after all killed, instrument per missing probe, open run → reproduce with human waits, open prediction run → fix/retest, unjudged → judge with optional await, judged → reproduce, confirmed without prediction run → fix/apply_fix, failed fix check → fix naming the run, fixed-check → cleanup listing probes, all removed → debug_end)
- [ ] 1.3 Fix-stage radius advice: `map_blast_radius` optional only when no radius and no prediction run; `annotate_blast_radius` optional with unreviewed count; tests for each
- [ ] 1.4 `closed_from`: evaluate precedence ignoring `closed_at`; tests for unsolved-mid-instrument and solved close
- [ ] 1.5 Anomaly pass (D-E) for all seven codes; tests per code, including a legacy state fixture (uncited confirm) loaded from JSON; assert anomalies never change `stage`
- [ ] 1.6 Determinism + purity: test that `Evaluate` on a deep-copied session leaves the input unchanged and returns identical results across calls; structural test (D-F) parsing `gates.go` and `blast_radius.go` failing on any reference to `Evaluate`/`Next`

## 2. Daemon

- [ ] 2.1 Session envelope gains `next`; every handler using `withReproRate` now also evaluates; tests over `httptest` asserting `next` on `GET` session and resume responses
- [ ] 2.2 State-changing `/api/` handlers (hypotheses, probes, probe removal, await, close run, hypothesis update, radius map/annotate) return `next` computed after the mutation; refused calls carry no `next`; one handler test per endpoint (ephemeral listener, never port 2218)

## 3. MCP

- [ ] 3.1 Add `Next *store.Next` to the output structs of `debug_resume`, `set_hypotheses`, `register_probe`, `remove_probe`, `await_run`, `close_run`, `update_hypothesis`, `map_blast_radius`, `annotate_blast_radius`, passed through from the daemon; `query_logs`/`diff_runs` untouched; tests asserting presence on each and absence on the read-only pair
- [ ] 3.2 One sentence in each affected tool description pointing the model at `next`; `docs/tools-reference.md` documents the `next` object once (shape + stage list) and references it from each affected tool (same-PR docs convention)

## 4. CLI

- [ ] 4.1 `cmd/sherlog/next.go`: `sherlog next [--session ID] [--json]`, stdlib `flag`, store opened directly (no daemon); default = most recently created open session; "no case is open" exit 0; unknown `--session` → error naming it; `usage` + README command list + `CLAUDE.md` binary-commands block updated
- [ ] 4.2 Text renderer: stage track line (`board ✓ instrument ✓ reproduce ● judge ○ fix ○ cleanup ○`), then ready / awaiting / anomalies blocks; golden-output tests with an injectable store root (never the real `~/.sherlog`)

## 5. Case Board

- [ ] 5.1 `detail.js` + `board.css`: stage track from the envelope's `next` (no client-side derivation), active stage labeled with the human wait when present, closed cases using `closed_from`; anomaly list section only when non-empty; light/dark via the existing `light-dark()` tokens
- [ ] 5.2 Verify in the browser against a real case at each stage (board, instrument, reproduce-waiting, judge, fix, cleanup, closed-unsolved), both themes; refresh the README screenshots if the detail view changed visibly

## 6. Skill + docs

- [ ] 6.1 `skills/debug/SKILL.md`: resume step 3 replaced by "read `next`"; per-step transitions reference `next`; anomalies surfaced to the user, never silently repaired; `next` is advice and gates stay authoritative; checklist updated
- [ ] 6.2 `docs/architecture.md`: evaluator vs gates section (advice vs enforcement, the structural test, why no card catalog)

## 7. Validation

- [ ] 7.1 Full local suite clean: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (gofmt prints nothing), plus `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] 7.2 Dogfood: `go install ./cmd/sherlog`, run a full `/debug` case on `examples/node-app` (after restoring its demo bug), `/clear` mid-case at the judge and fix stages, `/debug resume` → the skill continues at exactly the stage `next` reports; `sherlog next` and the Case Board agree with the tool result at each stage
- [ ] 7.3 `openspec validate add-case-next --strict` passes

## Workflow follow-up

- Archive with `/opsx:archive` after merge.
- If `add-probe-leak-guard` lands first, task 1.5's `closed-with-live-probes` reuses doctor's probes check; if this lands first, doctor's check calls this anomaly pass (design D-E).
- Next in the Stage-4 roadmap: `add-case-audit-and-stats`, which labels audit events and stats by these stage names.
