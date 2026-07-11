# blast-radius (delta)

## ADDED Requirements

### Requirement: The daemon executes the sibling search
Given a regex pattern, the daemon SHALL search file contents under the session `cwd` itself (stdlib regex engine, directory walk) and record the resulting hits as `{file, line, excerpt}` — the agent SHALL have no way to supply, add, or remove hits. An invalid regex SHALL be rejected with the compile error.

#### Scenario: Hits are daemon-recorded facts
- **WHEN** `map_blast_radius` is called with a pattern matching 4 sites under the session cwd
- **THEN** the stored radius contains exactly those 4 hits with file, line, and a trimmed excerpt, regardless of anything the agent claimed

#### Scenario: Invalid pattern rejected
- **WHEN** the pattern fails to compile
- **THEN** the tool returns the compile error and no radius is stored

### Requirement: The search is bounded and discloses truncation
The walk SHALL skip `.git` directories, files whose leading bytes indicate binary content, files above the size cap, and symlinks; it SHALL stop at the hit cap and set a `truncated` flag that is persisted and reported on every surface that shows the radius. The scan SHALL NOT block event ingest or waiting `await_run` calls.

#### Scenario: Truncated scan disclosed
- **WHEN** a pattern matches more sites than the hit cap
- **THEN** the stored radius holds the cap's worth of hits with `truncated: true`, and the tool result states the cap was reached

#### Scenario: Binary and .git content excluded
- **WHEN** the pattern would match bytes inside `.git/` or a binary file
- **THEN** those locations produce no hits

### Requirement: False-coverage gate
`map_blast_radius` SHALL be rejected when the board has no `confirmed` hypothesis, and rejected when the confirmed culprit's file — the file of the probe cited in the confirm citation — is absent from the hit set. The rejection SHALL name the culprit file and state that a pattern which misses the confirmed bug cannot establish sibling coverage. No override parameter SHALL exist.

#### Scenario: No confirmed suspect yet
- **WHEN** `map_blast_radius` is called while every hypothesis is `active` or `killed`
- **THEN** the call fails stating a confirmed root cause is required first

#### Scenario: Pattern that misses the culprit rejected
- **WHEN** the confirmed culprit sits in `src/auth.js` and the daemon-executed search finds no hit in that file
- **THEN** the call fails naming `src/auth.js`, and no radius is stored

#### Scenario: Pattern covering the culprit accepted
- **WHEN** the hit set includes the culprit file plus 3 other sites
- **THEN** the radius is stored and returned with all hits

### Requirement: Annotations are set-checked against recorded hits
`annotate_blast_radius` SHALL accept `{file, line, verdict, note?}` entries with `verdict` one of `sibling-bug`, `safe`, `already-covered`, and SHALL reject any entry whose `{file, line}` is not among the recorded hits. Partial annotation is valid; hits without a verdict SHALL be reported as `unreviewed`. Repeat annotations for the same `{file, line}` SHALL overwrite.

#### Scenario: Annotation of an unrecorded site rejected
- **WHEN** an annotation cites `{file: "src/other.js", line: 10}` which is not in the hit set
- **THEN** the call fails naming the unknown site and no annotation is applied

#### Scenario: Partial review disclosed
- **WHEN** 2 of 4 hits are annotated
- **THEN** the radius reports 2 hits as `unreviewed`

### Requirement: Re-running the search replaces the radius
A subsequent `map_blast_radius` call SHALL replace the stored radius entirely, clearing all annotations — verdicts on a previous search's hits SHALL NOT carry over.

#### Scenario: Refined pattern resets review state
- **WHEN** a radius with annotated hits exists and `map_blast_radius` is called with a new pattern
- **THEN** the stored radius reflects only the new search, with every hit `unreviewed`
