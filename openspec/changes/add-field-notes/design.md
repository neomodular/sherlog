# Design: add-field-notes

## Context

Sherlog's data model is case-centric and user-facing. Field notes are the opposite: tool-telemetry authored by the agent, read by the maintainer, invisible in the debug flow. The bug fixed by `fix-prerun-event-attribution` was discovered exactly this way — by an agent observation that nearly got lost.

## Goals / Non-Goals

**Goals:**
- Zero-friction filing from the skill (one tool call, never blocks the investigation).
- Strictly non-user-facing: not in `/debug` output, not in Case Board defaults, no mention to the user when filed.
- Trivially readable by the maintainer (`sherlog notes`).

**Non-Goals:**
- Automatic upload/telemetry anywhere — notes never leave the machine.
- Deduplication, threading, or analysis tooling (read the JSONL; revisit if volume demands).
- User-authored notes (agent channel only, v1).

## Decisions

### D1: Global JSONL file, not per-session
`~/.sherlog/field-notes.jsonl` with `{ts, session, version, category, note}`. Notes are about the *tool*, not the case; a single chronological file is exactly how a maintainer reads them. Session id is kept as context, not as the organizing key.

### D2: Dedicated `internal/notes` package
Tiny append/read package reusing the store's atomic-append pattern; keeps the case-centric store free of a second concern (SRP). Daemon mounts `POST /api/notes` + the MCP tool calls through the existing internal client.

### D3: Fire-and-forget semantics in the skill
The skill files a note and moves on — a failed filing must never interrupt an investigation (mirror of the probe philosophy). Tool result is minimal acknowledgment.

### D4: Category enum kept tiny
`tool-bug | friction | anomaly | other`. Categories exist solely for `sherlog notes --category`; resist taxonomy growth until reading pain demands it.

## Risks / Trade-offs

- [Note spam from over-eager filing] → skill rule scopes filing to *sherlog misbehavior*, not investigation difficulties; categories make skims cheap.
- [Sensitive values quoted in notes] → same locality guarantee as logs (`~/.sherlog/`, loopback-only); documented in README security notes.

## Migration Plan

Pure addition; absent file = no notes. Rollback = revert; the JSONL remains as inert local data.

## Open Questions

None.
