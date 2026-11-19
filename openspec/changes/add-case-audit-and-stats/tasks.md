# Tasks: add-case-audit-and-stats

> Prerequisites: `add-case-next` (stage, `closed_from`) and `add-probe-leak-guard` (guard refusal path) merged. Groups 1 and 4 can be built and tested before both land.

## 1. Audit log core

- [ ] 1.1 `internal/audit`: `Record` type per design D-B (common fields + kind-specific flat fields, `omitempty`), kind constants, schema `v:1`; marshal test pinning field names and asserting no free-text fields exist on the type (reflect over fields against an allowlist)
- [ ] 1.2 Writer: `O_APPEND|O_CREATE|O_WRONLY`, one `Write` per line, in-process mutex, failure reported once via `sync.Once` to stderr and never returned to callers; tests with an injectable root (never the real `~/.sherlog`), an unwritable-root case, and a concurrent-writers test asserting every line parses
- [ ] 1.3 Reader: streams lines, skips unparseable / unknown `v` / unknown `kind`, returns records plus a skipped count; tests for a truncated final line and a mixed-version file
- [ ] 1.4 Store emission: `Auditor` interface injected via a store `Option`; every mutating method records after `writeState` succeeds, off `s.mu`, with `stage` = `Evaluate` before the mutation; table test per mutation with a recording fake asserting kind, IDs, stage; benchmark showing evaluation cost is negligible beside `writeState`
- [ ] 1.5 Refusal emission: `gateCode` map (sentinel → stable kebab-case code) in `gates.go`; each gate failure records `refused` with tool + code + stage before returning; lookup errors record nothing; meta-test fails on a gate sentinel without a code (reuse the leak-guard corpus's sentinel parser); extend the bypass corpus to assert each attack also produced exactly one `refused` record with the right code
- [ ] 1.6 Retention: test that `PruneClosedBefore` leaves `audit.jsonl` byte-identical

## 2. Backfill

- [ ] 2.1 On daemon start with no `audit.jsonl`: synthesize records per D-E in timestamp order, `backfilled:true`, written to a temp file and renamed into place; stages from replaying `Evaluate` over a progressively rebuilt session copy; no `refused` records; tests: three fixture sessions (open, solved, unsolved) produce the expected sequence; existing file → no backfill; crash simulation (temp left behind) → backfill re-runs cleanly

## 3. Guard emission

- [ ] 3.1 The leak guard's refusal path appends one `guard_refused` record (layer, op, findings count, matched session IDs; no paths, no line content), best-effort and silent on failure; corpus cases assert the record on deny, none on allow, and an unchanged refusal when the log is unwritable

## 4. Stats

- [ ] 4.1 `internal/stats.Compute(records, now, abandonAfter) Report`: outcome classification per spec; verified solve rate with an explicit "unavailable" state; counts incl. in-flight and backfilled; table-driven tests for every outcome rule including solved-vs-solved-unverified and the abandonment boundary
- [ ] 4.2 Trend: ISO week (UTC) of each resolved case's terminal record, last 8 weeks with ≥1 resolved case; tests for quiet-week skipping and year boundaries (ISO week 52/53 → 1)
- [ ] 4.3 Loop health: first-try fix rate, median runs and time to solve, median kills before confirm, refusals per case and top-5 gate codes, guard refusals, closed cases with probes registered-not-removed, stopping-stage breakdown (from `case_closed.stage` for unsolved, last record's stage for abandoned); a test per metric over a shared fixture log
- [ ] 4.4 `definitions` text (outcomes + rate) as one constant used by both text footer and JSON; golden test

## 5. Config

- [ ] 5.1 `abandon_after_days` in `internal/config/keys.go` (Keys order, `ValueString`, set/validate positive int, default 7, file-only like other non-port keys); config tests for list/get/set and rejection of 0/negative/non-integer; `docs/configuration.md` row

## 6. CLI

- [ ] 6.1 `cmd/sherlog/audit.go`: `sherlog audit [--session ID] [--json]`, default most recently opened case in the log; timeline with refusals marked and backfilled records labeled; skipped-line count; golden-output tests
- [ ] 6.2 `cmd/sherlog/stats.go`: `sherlog stats [--json] [--since <dur>]` (`Nd`, `Nh`, Go durations); text report with rate, counts, trend, loop health, skipped/backfilled disclosure, definitions footer; golden-output tests; `usage`, README command list, `CLAUDE.md` binary-commands block

## 7. Daemon + Case Board

- [ ] 7.1 `/api/stats` gains `outcomes` from `stats.Compute` over the log; GET `/api/sessions/{id}/audit` returns that case's records; GET-only, no CORS; `httptest` tests (never assume port 2218)
- [ ] 7.2 `health.js`: Outcomes panel (rate or "not enough history", counts, trend bars, first-try fix rate, top refusals, leaks prevented, where cases die) rendered from `outcomes` only; `detail.js`: Timeline with refusals distinct and backfilled labeled; both themes via existing tokens; browser check against a populated log
- [ ] 7.3 `docs/audit-and-stats.md` (record schema, kinds, metadata-only rule, retention behavior, backfill, every definition); `docs/architecture.md` section on audit-as-history vs `state.json`-as-truth; README link; the gate code list in `docs/tools-reference.md` beside each tool's refusals

## 8. Validation

- [ ] 8.1 Full local suite clean: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (gofmt prints nothing), plus `GOOS=windows GOARCH=amd64 go build ./...`
- [ ] 8.2 Dogfood: `go install ./cmd/sherlog` on a machine with existing cases → backfill creates the log; run a full `/debug` case on `examples/node-app` including one deliberately refused confirm and one guard-refused commit; `sherlog audit` shows both refusals at their stages; `sherlog stats` and the Health view agree; prune a closed case (temporarily low `retention_days`) and confirm stats are unchanged
- [ ] 8.3 `openspec validate add-case-audit-and-stats --strict` passes

## Workflow follow-up

- Archive with `/opsx:archive` after merge, after `add-probe-leak-guard` and `add-case-next` are archived (its `probe-leak-guard` delta targets the spec that change creates).
- With this change, all five Stage-4 criteria have evidence; next is a `certification/README.md` + `WORKFLOW.md` change mapping each criterion to its artifact.
