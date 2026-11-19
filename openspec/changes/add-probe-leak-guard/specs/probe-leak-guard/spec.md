# probe-leak-guard

## Purpose

Prevent sherlog probe lines from leaving a developer's working tree in a commit or push, by detecting probe URLs in the content about to be committed or pushed and refusing the operation at two independent layers: the agent harness and git itself.

## ADDED Requirements

### Requirement: A probe is an added line carrying a known session's log URL
The guard SHALL treat an added line as a probe when it contains `/log/<id>/` where `<id>` is the ID of any session directory under the sherlog storage root, regardless of host, scheme, or port. Only added lines count; removed and context lines SHALL NOT trigger detection.

#### Scenario: Live session probe detected
- **WHEN** a staged change adds `fetch("http://127.0.0.1:2218/log/u8xggq67/p3", …)` and `u8xggq67` is a session directory on disk
- **THEN** the guard reports one finding naming the file, line, and session `u8xggq67`

#### Scenario: Removing a probe is not a finding
- **WHEN** a staged change deletes a line containing `/log/u8xggq67/p3`
- **THEN** the guard reports no finding

#### Scenario: Host-agnostic match
- **WHEN** an added line contains `http://localhost:9999/log/u8xggq67/p1` and `u8xggq67` is a known session
- **THEN** the guard reports a finding

### Requirement: Probes from pruned or foreign sessions are still detected by shape
The guard SHALL also treat an added line as a probe when it contains a loopback host (`127.0.0.1` or `localhost`) followed by `:<port>/log/` and a lowercase base36 token of at least 8 characters, so probes outliving retention or written on another machine are caught. Documentation placeholders such as `/log/<session>/` SHALL NOT match.

#### Scenario: Pruned session probe caught by shape
- **WHEN** an added line contains `http://127.0.0.1:2218/log/zz91k2ab/p2` and no session `zz91k2ab` exists on disk
- **THEN** the guard reports a finding marked as shape-matched

#### Scenario: Placeholder is not a probe
- **WHEN** an added line in a Markdown file contains `http://127.0.0.1:2218/log/<session>/<probe>`
- **THEN** the guard reports no finding

### Requirement: The harness hook refuses probe-carrying commits and pushes
When invoked as a Claude Code `PreToolUse` hook on a Bash call whose command invokes `git commit` or `git push` (including absolute-path git, `git -C <dir>`, and `-c key=value` prefixes), the guard SHALL scan what the command would commit or push and SHALL deny the call when any finding exists. Bash calls that are not commits or pushes SHALL be allowed without scanning.

#### Scenario: Commit with a staged probe denied
- **WHEN** the agent runs `git commit -m "fix"` with a staged probe line
- **THEN** the hook denies the call and the agent receives a message listing each finding

#### Scenario: Unrelated command passes untouched
- **WHEN** the agent runs `go test ./...`
- **THEN** the hook allows the call without reading the repository

#### Scenario: no-verify does not evade the harness layer
- **WHEN** the agent runs `git commit --no-verify -m "x"` with a staged probe line
- **THEN** the hook denies the call

### Requirement: Commits that take working-tree content are scanned on the working tree
For a commit command carrying `-a`, `--all`, `-i`, `--include`, `-o`, `--only`, or a pathspec, the harness hook SHALL scan tracked working-tree changes against `HEAD` in addition to the index, because those forms commit content not yet staged.

#### Scenario: commit -a with an unstaged probe denied
- **WHEN** a probe line exists only as an unstaged change and the agent runs `git commit -am "wip"`
- **THEN** the hook denies the call

### Requirement: A push is scanned over the commits it would publish
For a push, the guard SHALL scan the added lines of every commit reachable from the pushed ref that is not on the remote (the upstream range, or commits not on any remote-tracking ref when no upstream exists).

#### Scenario: Probe committed earlier is caught at push
- **WHEN** a probe was committed outside the agent session and the agent runs `git push`
- **THEN** the hook denies the push and names the commit containing the probe

### Requirement: The git layer enforces the same rule as git hooks
`sherlog guard install` SHALL write `pre-commit` and `pre-push` hooks into the current repository's hooks directory that run the same detection on the index (pre-commit) and on the pushed ranges git supplies (pre-push), exiting non-zero on any finding. It SHALL NOT overwrite an existing hook it did not write, and SHALL report what it skipped and how to chain manually.

#### Scenario: Terminal commit refused by the git layer
- **WHEN** the guard is installed and the user commits a staged probe from their own terminal
- **THEN** the commit is refused with the findings printed

#### Scenario: Foreign hook preserved
- **WHEN** the repository already has a `pre-commit` hook not written by sherlog
- **THEN** `sherlog guard install` leaves it unchanged, exits non-zero, and prints the line to add to it

#### Scenario: Install is idempotent
- **WHEN** `sherlog guard install` runs twice in the same repository
- **THEN** the second run rewrites its own hooks and reports success

### Requirement: The guard fails open and stays local
Any internal failure — unreadable storage root, missing `git`, malformed hook payload, a scan error — SHALL allow the operation and SHALL NOT block git. The guard SHALL NOT contact the daemon or the network and SHALL NOT modify the repository or the storage root, except `install` writing hook files and a refusal appending its audit record (case-audit).

#### Scenario: Missing storage root allows
- **WHEN** `~/.sherlog` does not exist and the agent runs `git commit`
- **THEN** shape detection still applies and, finding nothing, the call is allowed

#### Scenario: Malformed payload allows
- **WHEN** the hook receives input that is not valid JSON
- **THEN** it exits allowing the call

#### Scenario: No daemon required
- **WHEN** no sherlog daemon is running and a staged probe of a known session exists
- **THEN** the guard still detects and refuses it

### Requirement: Refusals state the repair
A refusal SHALL list each finding (file, line, session, and whether the session is open, closed, or unknown) and SHALL state the repair: remove the probe lines, or finish the case with `debug_end` and its cleanup. It SHALL NOT suggest disabling the guard.

#### Scenario: Refusal names the open case
- **WHEN** a commit is refused for a probe whose session is still open
- **THEN** the message names the session and says to remove the probe or finish the case before committing
