# Design: add-case-titles

## Context

`Session.Description` (set at `debug_start`) is the case's only human text; the Case Board, banner, recall, and resume all render it raw. Dogfooding showed long descriptions make case lists unscannable.

## Goals / Non-Goals

**Goals:**
- Scannable case identity everywhere (list, banner, recall, resume).
- Description becomes genuinely useful detail — structured when the information exists.
- Zero breakage for existing sessions and existing `debug_start` callers.

**Non-Goals:**
- Rigid structured fields (symptom/expected/repro as schema) — bugs arrive messy; forcing boxes produces fabricated structure.
- Auto-summarization in the daemon (the agent writes the title; the daemon only falls back to truncation).
- Editing titles after creation (cases are short-lived; revisit if needed).

## Decisions

### D1: Title is agent-authored, daemon-fallback
`debug_start` gains an optional `title` parameter. The skill MUST supply it (authorship rules below); if an older caller omits it, the daemon stores a fallback: first 60 chars of the description, word-boundary truncated with an ellipsis. This keeps the tool backward compatible while making good titles the norm.

### D2: Soft structure in the description, enforced by the skill not the schema
The skill writes the description as markdown-ish plain text with `Symptom:` / `Expected:` / `Repro:` / `Context:` lines, including only headings it has real content for. Storage and API treat it as one string. The UI renders heading lines bold when present (cheap regex, no parser).

### D3: Title display rules
- Lists, recall results, resume summaries, banner status line: title only.
- Case detail: title as header, full description below.
- Legacy title-less sessions: the daemon's fallback derivation happens at read time for old `state.json` files (no migration write needed) so every payload always carries a non-empty title.

### D4: Skill authorship rules
Title: imperative or noun-phrase summary of the *failure*, ≤ 60 chars, specific ("Login 401 after idle timeout", not "Bug in auth" or the full story). Description: distill the user's report under the soft headings; quote exact error text in Symptom when available; never invent Expected/Repro the user didn't state — ask only if symptom or expected is genuinely unclear, otherwise proceed.

### D5: Recall corpus
Title joins description + root cause + confirmed hypothesis in the scored text. No weighting changes (titles are short, so their tokens are naturally high-signal under TF overlap).

## Risks / Trade-offs

- [Agent writes vague titles] → authorship rules in the skill with good/bad examples; reviewers of future sessions see titles in the board, creating natural pressure.
- [Fallback truncation mid-thought for legacy cases] → word-boundary truncation + ellipsis; cosmetic only, legacy cases age out.
- [Heading regex in UI mis-renders user text] → render-only enhancement; worst case a line is bold that shouldn't be.

## Migration Plan

Additive `title` in `state.json` (omitted = legacy, derived at read). Tool schema change is additive-optional. No data migration; rollback = revert, stored titles are ignored by older binaries.

## Open Questions

None.
