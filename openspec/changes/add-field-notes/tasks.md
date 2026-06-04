# Tasks: add-field-notes

## 1. Notes package + daemon

- [ ] 1.1 `internal/notes`: append/read JSONL at ~/.sherlog/field-notes.jsonl (injectable root), typed record {ts, session, version, category, note}, category validation; unit tests incl. absent-file reads
- [ ] 1.2 Daemon `POST /api/notes` endpoint wired to the package

## 2. MCP + CLI

- [ ] 2.1 `report_observation(note, category)` tool — fire-and-forget semantics, minimal ack, current session id attached when active
- [ ] 2.2 `sherlog notes [--category <c>]` subcommand, chronological output, empty-safe
- [ ] 2.3 Tests: tool→daemon→file roundtrip, CLI filter

## 3. Skill + docs

- [ ] 3.1 SKILL.md: misbehavior-filing rule (tool behavior only, silent, never blocks), examples of what does/doesn't qualify
- [ ] 3.2 README security note: field notes are local-only, what they may contain
