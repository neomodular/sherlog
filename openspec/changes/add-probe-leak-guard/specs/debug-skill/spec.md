# debug-skill (delta)

## ADDED Requirements

### Requirement: A leak-guard refusal is repaired, never bypassed
When a commit or push is refused by the probe-leak guard, the skill SHALL treat it as the guard doing its job: either remove the listed probe lines (and mark them removed) or finish the case through the cleanup gate, then retry. It SHALL NOT use `--no-verify`, alter hook configuration, rewrite the probe URL to evade detection, or report the refusal as a sherlog malfunction.

#### Scenario: Mid-case commit refused
- **WHEN** the user asks to commit an unrelated change while probes are live and the guard refuses
- **THEN** the skill tells the user which probes block the commit and offers to unstage them or commit only the unrelated files, without bypassing the guard

### Requirement: The git layer is offered, not imposed
At case close, when the current repository has no sherlog git hooks installed, the skill SHALL mention once that `sherlog guard install` adds the git-level layer for commits made outside Claude Code, and SHALL NOT run it without the user's consent.

#### Scenario: Offer made once at close
- **WHEN** a case closes in a repository without the sherlog hooks
- **THEN** the closing summary includes a one-line offer of `sherlog guard install` and the skill does not install it on its own
