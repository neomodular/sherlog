# debug-skill (delta)

## ADDED Requirements

### Requirement: In witnessed mode the user gives the verdict
When `next` shows the verdict awaiting the user's terminal command, the skill SHALL ask the user to run `sherlog verdict` in their own terminal outside Claude Code, explain that the `!` prefix will not work because it runs inside the agent session, and SHALL NOT call `close_run` or run the command itself. After the user reports it is done, the skill SHALL continue from the recorded state (`debug_resume` / `next`), not from the user's message.

#### Scenario: Retest in witnessed mode
- **WHEN** a fix retest's await returns in witnessed mode
- **THEN** the skill asks the user to run `sherlog verdict` in their own terminal and, once told it is done, reads the recorded verdict through `debug_resume`

#### Scenario: Refused close is not retried
- **WHEN** the agent's `close_run` is refused for witnessed mode
- **THEN** the skill asks the user for the terminal command and does not retry the tool or try to change the mode
