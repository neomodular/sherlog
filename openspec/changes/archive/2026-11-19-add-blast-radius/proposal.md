# Proposal: add-blast-radius

## Why

A confirmed root cause is rarely a lone occurrence — the same anti-pattern usually lives at sibling call sites. Today sherlog's loop ends at fix-verify-cleanup, and any "I checked for other occurrences" is an unrecorded agent claim: not runnable, not re-checkable, fabrication-prone in both directions (invented sites and missed sites). The trust model that makes probe evidence reliable — the daemon records facts, the agent interprets them — extends naturally here: let the daemon run the sibling search itself, so the hit list is a recorded fact and the only thing the agent contributes is the pattern and the judgment per hit.

Depends on `harden-detective-gates`: the false-coverage gate cross-checks the pattern against the confirmed hypothesis's validated evidence citation.

## What Changes

- **New MCP tool `map_blast_radius(session_id, pattern, note?)`** — after a hypothesis is confirmed, the agent proposes a regex sibling pattern; the **daemon executes the search** against the session `cwd` (stdlib `regexp` + `filepath.WalkDir`; skips `.git`, binaries, oversized files, symlinks; bounded hit count with truncation disclosed) and stores the hits. Nothing to fabricate, nothing to under-report.
- **False-coverage gate** — the tool is rejected unless the board has a `confirmed` hypothesis, and rejected when the confirmed culprit's probe file is absent from the hit set (a pattern that misses the known bug proves nothing about siblings). The skill runs the search **after confirm, before the fix**, while the anti-pattern still exists at the culprit site.
- **New MCP tool `annotate_blast_radius(session_id, annotations[])`** — the agent grades each hit (`sibling-bug` | `safe` | `already-covered`, optional note); the daemon accepts annotations only for hits it recorded. Unannotated hits remain visibly `unreviewed`.
- **Persistence** — the radius (pattern, note, hits with annotations, searched-at, truncation flag) lives on the session as an additive `state.json` field; a re-run replaces the previous radius.
- **Case Board** — case detail and closed-case view render the radius: pattern, hit list with verdict badges, unreviewed count, truncation disclosure.
- **Recall** — the sibling pattern text joins the recall corpus, so future cases can match on the defect pattern, not just the symptom prose.
- **Skill** — propose the pattern right after confirm; annotate every hit honestly; never claim sibling coverage without a recorded search; `debug_end` is untouched — the radius is optional and never gates the close.

Out of scope: fixing sibling sites, data-corruption remediation plans, cross-repo search, non-regex query engines, and any execution of user code.

## Capabilities

### New Capabilities

- `blast-radius`: daemon-executed sibling-occurrence search with the false-coverage gate, hit annotation, and bounded-scan semantics.

### Modified Capabilities

- `session-state`: ADDED — blast radius persisted on the session (additive field, replace-on-re-run); legacy state loads unchanged.
- `mcp-server`: ADDED — `map_blast_radius` and `annotate_blast_radius` tool schemas; gate and validation errors surfaced actionably.
- `debug-skill`: ADDED — when to search (post-confirm, pre-fix), pattern authorship, honest annotation, no unrecorded coverage claims.
- `case-board-ui`: ADDED — radius rendering (pattern, verdict badges, unreviewed count, truncation).
- `case-recall`: ADDED — sibling pattern text included in the searched/scored corpus.

## Impact

- Code: `internal/store` (radius model + persistence + gates), `internal/daemon` (search executor + API), `internal/mcp` (two new tools), `internal/daemon/ui/` (radius section), `skills/debug/SKILL.md`, `docs/tools-reference.md` + `docs/architecture.md` (docs-update convention).
- The daemon gains read access to file *contents* under the session cwd (today it only stats probe locations per `harden-detective-gates`). Read-only, local-only, never uploaded — the security invariants hold; Go's RE2 engine keeps untrusted patterns linear-time.
- Backward compatible on disk (additive field) and on the tool contract (two new tools; no existing tool changes).
