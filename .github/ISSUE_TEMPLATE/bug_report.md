---
name: Bug Report
about: Report a bug or unexpected behavior in sherlog
title: ""
labels: bug
assignees: ""
---

## Description

A clear and concise description of the bug.

## Steps to Reproduce

1. Start a debug session (`/debug ...`) or daemon command: ...
2. Do this: ...
3. Observe the error or incorrect behavior.

## Expected Behavior

What you expected to happen.

## Actual Behavior

What actually happened. Include error messages, MCP tool errors, or unexpected output.

## Environment

- **sherlog version** (`sherlog --version`):
- **Install method**: brew / `go install` / built from source
- **Operating system**:
- **Claude Code version** (`claude --version`, if the bug involves the plugin/skill):
- **Probe runtime** (if the bug involves probes): Node / browser / Python / Go / other

## Daemon State (if the daemon is involved)

- Output of `http://127.0.0.1:2218/health` (or a screenshot of `#/health` in the Case Board):
- Any failing self-checks shown on the health page:

> Tip: run `sherlog notes` — the agent files internal observations when sherlog
> misbehaves, and one may already describe your issue. Paste anything relevant.

## Minimal Reproduction

```text
The smallest sequence (commands, probe line, tool call) that reproduces the issue.
```

## Additional Context

Any other information that might help diagnose the issue (logs, screenshots, related issues).
