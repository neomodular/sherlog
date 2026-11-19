# Tasks: add-blast-radius

## 1. Store — model + gates

- [x] 1.1 Types: `BlastRadius {Pattern, Note, SearchedAt, Truncated, Hits[]}`, `BlastHit {File, Line, Excerpt, Verdict, Note}` on Session — additive JSON; round-trip + legacy-load tests (injectable root dir, files not rewritten)
- [x] 1.2 Gate: radius set only when the board has a `confirmed` hypothesis; culprit file (from the confirm citation's probe) must be in the hit set; typed errors naming the culprit file; tests for no-confirm, culprit-missing, culprit-present
- [x] 1.3 Replace semantics: re-run swaps the whole radius and clears annotations; test that verdicts never carry over
- [x] 1.4 Annotation merge: set-membership check against recorded hits, verdict enum validated, overwrite-by-{file,line}, unreviewed derivation; tests incl. unknown-site rejection and partial review

## 2. Daemon — search executor + API

- [x] 2.1 Bounded walker: stdlib `regexp` + `filepath.WalkDir` from session cwd; skip `.git`, NUL-sniffed binaries (first 8 KB), files >2 MB, symlinks; 500-hit cap sets `Truncated`; excerpt trim ~200 chars; runs outside the store lock; table-driven tests over a temp tree (never the real repo, never assume port 2218)
- [x] 2.2 `/api/` endpoints for search + annotate; invalid regex → compile error verbatim; gate/validation failures → 4xx with repair instruction; httptest coverage per rejection
- [x] 2.3 Concurrency test: a long scan does not block `/log/` ingest or an open `await_run`

## 3. MCP — tools

- [x] 3.1 `map_blast_radius(session_id, pattern, note?)` returning hits + truncation + unreviewed count; daemon errors surfaced verbatim
- [x] 3.2 `annotate_blast_radius(session_id, annotations[])` with client-side verdict enum validation before the daemon round-trip

## 4. Case Board UI

- [x] 4.1 Radius section on case detail + closed-case view: pattern, hits with verdict badges (`sibling-bug`/`safe`/`already-covered`/`unreviewed`), unreviewed count, truncation notice; inert text only (GET-only invariant); section omitted when no radius; asset/route tests

## 5. Recall

- [x] 5.1 Radius pattern text joins the recall corpus for solved cases; hit file paths excluded; recall-by-pattern test + path-not-matchable test

## 6. Skill + docs (same PR, docs-match-binary convention)

- [x] 6.1 SKILL.md: map the radius after confirm and **before the fix** (anti-pattern still present at the culprit); pattern targets the mechanism, not symptom text; annotate every hit honestly (`safe` legitimate, unjudgeable stays `unreviewed` and is said aloud); refine rejected patterns instead of restating coverage in prose
- [x] 6.2 SKILL.md: no sibling-coverage claims without a recorded radius; report from the board with counts; surface `sibling-bug` hits to the user before `debug_end` (their call: fix now or track) — never block the close on the radius
- [x] 6.3 `docs/tools-reference.md`: both new tool schemas; `docs/architecture.md`: daemon read surface (file contents under session cwd), scan bounds, false-coverage gate rationale

## 7. Validation

- [x] 7.1 Full local suite green: `go build ./... && go vet ./... && go test ./... && gofmt -l .` (empty), plus `go test -race ./...`
- [x] 7.2 End-to-end dogfood: `go install`, kill resident daemon, run a `/debug` case to a real confirm, then exercise the radius — culprit-missing rejection, valid search, annotation of a fake site rejected, truncation on a broad pattern — and check the Case Board renders the section correctly — done via 3-trial live dogfood (wf_410e86cf-854; 2 trials clean, gates non-mutating and errors actionable from text alone) plus a hand-run bounds attestation after one trial agent died: 500-hit cap with `truncated:true` on 523 planted matches, zero `.git`/binary hits, 21 ms probe ingest during a live scan, radius on the board detail, registry left clean
