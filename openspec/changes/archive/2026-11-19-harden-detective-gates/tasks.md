# Tasks: harden-detective-gates

## 1. Store — types + gates (the enforcement core)

- [x] 1.1 Types: `Probe.ExpectedIfTrue/ExpectedIfFalse`, `Hypothesis.EvidenceProbeID/EvidenceRunID`, `Run.Prediction/PredictionAt`, `Session.Commit`, `Resolution.RegressionTestRef/Guardrail{Type,Ref}` — all additive JSON fields; table-driven round-trip + legacy-state-load tests (injectable root dir, files not rewritten)
- [x] 1.2 Prediction-pair validation on probe registration: both-or-neither, must differ (trim + case-insensitive); typed errors; tests for valid pair, equal pair, one-sided
- [x] 1.3 Evidence-citation gate on `UpdateHypothesis`: kill/confirm require probe+run; probe registered, run exists and closed; zero-fired citation valid; citation persisted; `active` exempt; tests incl. unknown run, open run, missing citation
- [x] 1.4 Confirm gate: ≥1 closed `reproduced` run in session AND cited probe carries predictions; tests for both rejection paths + fully qualified confirm
- [x] 1.5 `SetHypotheses` rejects <3 statements, board unchanged on rejection; test
- [x] 1.6 Run prediction: stamped at receipt before summary return, immutable once set; `CloseRun(fixed-check)` rejected without prediction with repair-instruction error; tests
- [x] 1.7 Solved-close validation in `CloseSessionWithResolution`: any resolution field → all of root_cause/fix_summary/confirmed_hypothesis_id required AND id must be board-`confirmed`; rejection leaves session open (no silent unsolved downgrade); guardrail type enum validated; unsolved close untouched; tests
- [x] 1.8 Computed repro rate helper: `reproduced/(reproduced+not-reproduced)` over closed runs excluding fixed-check, never stored, returned with raw counts; test
- [x] 1.9 Commit pinning at session create: fixed-argv `git -C <cwd> rev-parse HEAD`, short timeout, silent omit on any failure; tests for repo and non-repo cwd (tests must not assume the test env is a repo)

## 2. Daemon — API surface

- [x] 2.1 `/api/` handlers pass new params through (register_probe predictions, update_hypothesis citations, await_run prediction, debug_end refs); store gate errors → 4xx with the one-line repair instruction; httptest coverage for each rejection
- [x] 2.2 Probe location check at registration: resolve `file` against session cwd (absolute passes through), reject missing file / out-of-range line with resolved path in the error; tests use a temp dir as session cwd — never the real repo
- [x] 2.3 Payloads expose `commit`, repro rate + counts (await_run result, resume, session detail), run predictions, resolution refs; `diff_runs` includes the fixed-check run's prediction

## 3. MCP — tool schemas

- [x] 3.1 `register_probe`: `expected_if_true`/`expected_if_false` params; `update_hypothesis`: `probe_id`/`run_id` params with client-side required-when-kill/confirm check; `await_run`: `prediction`; `debug_end`: `regression_test_ref` + `guardrail`; daemon errors surfaced verbatim as tool errors
- [x] 3.2 `debug_start` result carries the pinned commit; `debug_resume`/`await_run` results carry repro rate with counts

## 4. Case Board UI

- [x] 4.1 Probe listing renders prediction pairs (if-true / if-false, visually paired); hypothesis verdicts show citation via display names ("Probe 1 · Run 2") beside the note
- [x] 4.2 Case header shows repro rate `n/m` (once ≥1 repro-attempt run closed) and short commit; run detail + compare view render the fixed-check prediction above the divergence list
- [x] 4.3 Resolution panel shows regression-test ref and guardrail type badge + ref as inert text (GET-only, no external origins); asset/route tests updated

## 5. Skill + docs (same PR, docs-match-binary convention)

- [x] 5.1 SKILL.md: prediction authorship per discriminating probe (every hypothesis has ≥1 predicted probe before the wait), structured citations on every kill/confirm, fix prediction via `await_run(prediction=...)` before fixed-check, repro-rate-not-asserted rule, prevention refs only when real
- [x] 5.2 SKILL.md: gate-rejection posture — perform the named repair and retry; never weaken the claim, close unsolved to bypass, or retry verbatim
- [x] 5.3 `docs/tools-reference.md` for every schema change; `docs/architecture.md` gains a "mechanical gates" section (what each gate catches, the D-D loophole, threat model)

## 6. Validation

- [x] 6.1 Full local suite green: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (empty), plus `go test -race ./...`
- [x] 6.2 End-to-end dogfood: `go install`, kill resident daemon, run a real `/debug` loop exercising one rejection per gate (small board, citation-less kill, prediction-less confirm, prediction-less fixed-check, invalid solved close) and confirm each error's repair instruction is actionable — done via 6-trial live dogfood (wf_2bd4c2b2-e51): all five trips repaired from error text alone; found and fixed one gate bypass (out-of-enum hypothesis status, now `ErrInvalidHypothesisStatus` + regression tests) plus strict `/api/` decoding and cases-feed repro_rate; fixes re-verified against the live daemon
