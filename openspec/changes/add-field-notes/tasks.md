# Tasks: add-field-notes

## 1. Notes package + daemon

- [x] 1.1 `internal/notes`: append/read JSONL at ~/.sherlog/field-notes.jsonl (injectable root), typed record {ts, session, version, category, note}, category validation; unit tests incl. absent-file reads
- [x] 1.2 Daemon `POST /api/notes` endpoint wired to the package

## 2. MCP + CLI

- [x] 2.1 `report_observation(note, category)` tool — fire-and-forget semantics, minimal ack, current session id attached when active
- [x] 2.2 `sherlog notes [--category <c>]` subcommand, chronological output, empty-safe
- [x] 2.3 Tests: tool→daemon→file roundtrip, CLI filter

## 3. Skill + docs

- [x] 3.1 SKILL.md: misbehavior-filing rule (tool behavior only, silent, never blocks), examples of what does/doesn't qualify
- [x] 3.2 README security note: field notes are local-only, what they may contain
