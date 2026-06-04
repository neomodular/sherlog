# Proposal: add-field-notes

## Why

During dogfooding, the agent made a precise observation about sherlog's own behavior ("pre-run events may need buffering") that surfaced a real bug — and it survived only because the user happened to read it in chat. Agent observations about the tool itself (suspected bugs, friction, anomalies) currently have no home: they are not user-facing case material, and they vanish with the conversation.

## What Changes

- New MCP tool `report_observation(note, category)` (categories: `tool-bug`, `friction`, `anomaly`, `other`) the agent calls when sherlog itself behaves unexpectedly.
- Daemon appends observations to `~/.sherlog/field-notes.jsonl` (timestamp, session id, sherlog version, category, note) — a maintainer's inbox, global across sessions.
- New CLI `sherlog notes` (with `--category` filter) to read them.
- Field notes are hidden from `/debug` output and any user-facing views; the skill files notes silently.
- The `/debug` skill gains the rule: file an observation whenever sherlog misbehaves (zero events despite repro, await oddities, cleanup surprises) — then continue the investigation normally.

## Capabilities

### New Capabilities

- `field-notes`: The observation channel — MCP tool, daemon storage, CLI reader, and non-user-facing guarantee.

### Modified Capabilities

- `debug-skill`: ADDED — files observations on unexpected sherlog behavior without surfacing them to the user.

## Impact

- Code: `internal/store` or a small `internal/notes` package, daemon endpoint, `internal/mcp` (new tool), `cmd/sherlog` (notes subcommand), `skills/debug/SKILL.md`.
- Privacy: notes may quote investigation context; they stay local in `~/.sherlog/` like everything else. Documented.
- No behavior change for end users.
