# documentation

## ADDED Requirements

### Requirement: Reference documentation set
The repository SHALL contain `docs/` with: `probe-contract.md` (canonical one-liner per language — JS/browser, Node, Python, Go, Ruby, curl — fire-and-forget rules, no-JSON-content-type rule, URL anatomy, greppability), `tools-reference.md` (every shipped MCP tool: purpose, parameters, result shape, example), `troubleshooting.md`, `architecture.md` (component diagram, storage layout, await/debounce and flood-control semantics in present tense), `configuration.md` (every config key, precedence, CLI usage), and `brand.md` (mascot sprite glyphs, colors, vocabulary, usage rules).

#### Scenario: Looking up a probe form
- **WHEN** a user opens docs/probe-contract.md needing a Python probe
- **THEN** they find a copy-pasteable one-liner with the fire-and-forget and content-type rules explained

#### Scenario: Tool reference completeness
- **WHEN** the shipped MCP tool list is compared against docs/tools-reference.md
- **THEN** every tool has an entry documenting parameters and result shape

### Requirement: Troubleshooting covers known failure modes
`troubleshooting.md` SHALL be organized by observable symptom and SHALL cover at minimum: port 2218 conflict (foreign process, SHERLOG_PORT), zero events after reproduction (daemon down, app not restarted/rebuilt, probe URL session mismatch), `sherlog` not on PATH (plugin MCP launch failure), stale probes discovery/removal, and where session data lives on disk.

#### Scenario: Diagnosing zero events
- **WHEN** a user sees an await return zero events and opens troubleshooting.md
- **THEN** the symptom entry walks the /health check, app-restart check, and session-URL match in order

### Requirement: README links the docs
README SHALL retain only install + a 60-second tour and link each docs/ page for depth, and SHALL state the review convention that tool/config changes update their reference page in the same PR.

#### Scenario: Finding depth from the README
- **WHEN** a reader finishes the README tour
- **THEN** each deeper topic (probes, tools, config, troubleshooting, architecture, brand) is one link away
