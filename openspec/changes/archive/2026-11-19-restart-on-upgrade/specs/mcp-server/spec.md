# mcp-server (delta)

## ADDED Requirements

### Requirement: Version mismatch is disclosed, never enforced
When the daemon health check succeeds but the reported version differs from the MCP process's own compiled version, the client SHALL log one informational line per process stating that the daemon and client versions differ and that a session restart loads the new tool schemas. The client SHALL NOT restart, kill, or refuse to use the daemon based on version comparison.

#### Scenario: Stale client notes the difference once
- **WHEN** an MCP process built as v0.8.0 talks to a daemon reporting v0.9.0 across many tool calls
- **THEN** exactly one informational line is emitted and every tool call proceeds normally

#### Scenario: Matching versions stay silent
- **WHEN** client and daemon report the same version
- **THEN** no note is emitted
