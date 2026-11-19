# Tasks: add-case-titles

## 1. Store + daemon

- [x] 1.1 `Session.Title` (additive, persisted); read-time fallback derivation (word-boundary ~60 chars + ellipsis) so payloads always carry a title; legacy state files load unchanged and are not rewritten
- [x] 1.2 Title in recall corpus and recall result payloads
- [x] 1.3 Daemon payloads (session list/detail, resume, related-cases) expose title distinctly; tests for round-trip, legacy derivation, recall-by-title

## 2. MCP + skill

- [x] 2.1 `debug_start` optional `title` param (backward compatible), echoed in response; titles in `debug_resume` and related-cases payloads
- [x] 2.2 SKILL.md: title authorship rules (≤60 chars, specific, good/bad examples), soft-structured description format (Symptom/Expected/Repro/Context, only real content, quote exact errors, one clarifying question max), banner shows title
- [x] 2.3 docs/tools-reference.md updated for the debug_start schema change (docs-update convention)

## 3. UI

- [x] 3.1 Cases list and all case references show title; detail view = title header + description with heading lines bolded (render-only regex); legacy fallback display; asset/route tests updated
