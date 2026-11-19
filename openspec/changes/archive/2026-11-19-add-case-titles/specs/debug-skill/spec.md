# debug-skill (delta)

## ADDED Requirements

### Requirement: Skill authors the title
At `debug_start` the skill SHALL author a title: an imperative or noun-phrase summary of the failure, at most ~60 characters, specific to the observed problem (e.g. "Login 401 after idle timeout" — never "Bug in auth" or a full sentence of narrative). The skill banner status line SHALL show the title.

#### Scenario: Title authored from a rambling report
- **WHEN** the user reports a multi-paragraph bug story
- **THEN** the skill distills a ≤60-char specific title and starts the session with it, and the banner shows the title

### Requirement: Soft-structured description
The skill SHALL write the description as plain text under the headings `Symptom:`, `Expected:`, `Repro:`, `Context:` — including only headings it has real content for, quoting exact error text in Symptom when available, and never inventing Expected/Repro the user did not state. The skill SHALL ask the user for clarification only when the symptom or expected behavior is genuinely unclear; otherwise it proceeds.

#### Scenario: Partial information stays honest
- **WHEN** the user describes a symptom and expected behavior but no reproduction steps
- **THEN** the description contains Symptom and Expected sections only — no fabricated Repro

#### Scenario: Unclear symptom triggers one question
- **WHEN** the user's report does not establish what actually goes wrong
- **THEN** the skill asks a clarifying question before debug_start instead of guessing
