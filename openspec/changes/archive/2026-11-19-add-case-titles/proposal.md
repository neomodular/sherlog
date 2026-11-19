# Proposal: add-case-titles

## Why

A case's only text field today is the bug description passed to `debug_start`, and it does two jobs at once: case identity and full context. In practice descriptions are long, so the Case Board list, the skill banner, and recall results all show a paragraph where a title belongs — cases are hard to scan and hard to tell apart (observed during dogfooding).

## What Changes

- Sessions gain a **title**: a short, specific summary (target ≤ 60 chars) shown everywhere a case is referenced — Case Board lists, case detail header, the skill banner status line, recall results, and `debug_resume` output.
- The **description** remains the detailed narrative, upgraded with *soft structure*: the skill writes it with `Symptom / Expected / Repro / Context` headings when the information exists — guidance in the skill, plain text in storage, never fabricated fields.
- `debug_start` accepts `title` + `description`; the skill is responsible for authoring a good title (imperative, specific, no trailing prose) and distilling the user's report into the structured description, asking the user only when symptom/expected are genuinely unclear.
- Recall search includes the title (titles are high-signal tokens).
- Backward compatible: sessions without a title fall back to a truncated description wherever titles display.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `session-state`: ADDED — title field on sessions, persisted; title-less legacy sessions remain valid.
- `mcp-server`: ADDED — `debug_start(title, description)`; title in `debug_resume`/related-cases payloads.
- `case-recall`: ADDED — title included in the searched/scored text.
- `case-board-ui`: ADDED — lists and headers show title; detail shows the structured description; truncated-description fallback for legacy sessions.
- `debug-skill`: ADDED — title authorship rules and soft-structured description format.

## Impact

- Code: `internal/store` (Session.Title, recall corpus), `internal/daemon` (payloads), `internal/mcp` (tool schema), `internal/daemon/ui/` (list/detail rendering), `skills/debug/SKILL.md`, `docs/tools-reference.md` (docs-update convention).
- Backward compatible end to end: `state.json` field is additive; `debug_start` keeps working if a caller omits `title` (daemon derives a truncated fallback).
