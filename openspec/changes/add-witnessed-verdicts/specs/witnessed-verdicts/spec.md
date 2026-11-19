# witnessed-verdicts

## Purpose

Make the user's verdict on a reproduction a human act that sherlog can tell apart from an agent's assumption: record where every verdict came from, give the user a terminal command to give it, and optionally refuse verdicts from the agent.

## ADDED Requirements

### Requirement: Every closed run records its verdict source
Closing a run SHALL record `verdict_source`: `agent` when closed through the `close_run` tool, `witness` when closed through the `sherlog verdict` command. Runs closed before this capability existed SHALL report the source as `unknown`.

#### Scenario: Agent close in default mode
- **WHEN** the agent calls `close_run(verdict: reproduced)` with `verdicts` set to `agent`
- **THEN** the run closes with verdict `reproduced` and source `agent`

#### Scenario: Legacy run
- **WHEN** a state file from before this change is loaded
- **THEN** its closed runs report source `unknown`

### Requirement: The verdict command shows the evidence, then asks
`sherlog verdict` with no argument SHALL show the current case's title, the open run, its per-probe event counts, and for a run carrying a fix prediction, that prediction; then prompt for `reproduced`, `not-reproduced`, or `fixed-check` and close the run with source `witness`. With an argument it SHALL close the run with that verdict without prompting. `--session ID` selects a case other than the most recently created open one.

#### Scenario: Fix retest shows the claim being judged
- **WHEN** the user runs `sherlog verdict` while the open run carries the prediction "p1 populated; p5 fires zero times"
- **THEN** the prediction and the per-probe counts are shown before the prompt

#### Scenario: No open run
- **WHEN** the current case has no open run
- **THEN** the command says so, exits non-zero, and changes nothing

### Requirement: The verdict command refuses agent sessions
`sherlog verdict` SHALL refuse to run, changing nothing, when the `CLAUDECODE` environment variable is set or standard input is not a terminal, and the refusal SHALL tell the user to run it in their own terminal outside Claude Code.

#### Scenario: Agent tries the command
- **WHEN** the agent runs `sherlog verdict fixed-check` through its Bash tool
- **THEN** the command exits non-zero, the run stays open, and the message says the verdict must come from the user's own terminal

#### Scenario: Bang prefix is inside the session
- **WHEN** the user types `! sherlog verdict reproduced` in Claude Code
- **THEN** the command refuses, because it runs inside the agent session

### Requirement: Witnessed mode refuses agent verdicts at the tool
When `verdicts` is `witnessed`, the daemon SHALL refuse a run close whose source is `agent`, leaving the run open, with a message naming the command the user must run. The refusal SHALL be a gate refusal recorded like other gate refusals.

#### Scenario: Agent close refused
- **WHEN** `verdicts` is `witnessed` and the agent calls `close_run(verdict: fixed-check)`
- **THEN** the call is refused, the run stays open, and the message tells the user to run `sherlog verdict` in their own terminal

### Requirement: Witnessed mode refuses the command at the harness
When `verdicts` is `witnessed`, the harness hook SHALL deny an agent Bash call that invokes `sherlog verdict`, independently of the command's own environment check.

#### Scenario: Hook denies before the CLI runs
- **WHEN** `verdicts` is `witnessed` and the agent's Bash command is `sherlog verdict reproduced`
- **THEN** the hook denies the call and the command never starts

### Requirement: Only the user switches the mode
No MCP tool SHALL read or change `verdicts`; it SHALL change only through the config file or `sherlog config set`, and in witnessed mode the hook SHALL deny an agent Bash call that runs `sherlog config set verdicts`.

#### Scenario: Agent tries to turn witnessed mode off
- **WHEN** `verdicts` is `witnessed` and the agent runs `sherlog config set verdicts agent`
- **THEN** the hook denies the call and the mode stays `witnessed`

### Requirement: The limit is documented, not hidden
The documentation SHALL state that the agent runs as the same OS user and can therefore forge a witnessed close deliberately (for example by crafting the daemon request), that witnessed mode stops the casual path rather than a determined bypass, and that the recorded source makes provenance visible either way.

#### Scenario: Residual gap stated
- **WHEN** a user reads the witnessed-verdicts documentation
- **THEN** it names the same-user limit and what witnessed mode does and does not prevent
