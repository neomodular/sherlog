# mcp-server (delta)

## ADDED Requirements

### Requirement: Version-mismatch self-heal
The MCP client SHALL compare the daemon's `/health` version against its own build version whenever it ensures the daemon is up. On mismatch it SHALL replace the stale daemon transparently: `POST /api/shutdown`, poll until the port refuses a fresh dial (bounded, ~3 s), then respawn via the existing detached spawn and wait for health. Matching versions (including `dev` = `dev`) SHALL leave the running daemon untouched. The spawned daemon is the MCP process's own executable, so the replacement always answers with the MCP's version.

#### Scenario: Upgrade self-heals on next tool call
- **WHEN** the binary is upgraded while an old daemon runs, and any MCP tool call ensures the daemon
- **THEN** the old daemon is shut down and a daemon answering /health with the MCP's own version is serving before the tool proceeds

#### Scenario: Same version is a no-op
- **WHEN** the daemon's /health version equals the MCP's version
- **THEN** ensure-up succeeds without shutdown, spawn, or any restart side effect

#### Scenario: Concurrent restarts converge
- **WHEN** two MCP processes detect the mismatch and race the shutdown/spawn handshake
- **THEN** both end healthy against a single new daemon — the losing spawn's bind failure is not surfaced to either user

### Requirement: Legacy daemon actionable error
When the running daemon predates the shutdown endpoint (404 on `POST /api/shutdown`), the MCP client SHALL fail the ensure with an error that names the daemon and client versions and the exact one-time manual stop command for the platform (`pkill -f "sherlog daemon"` on macOS/Linux, `Get-Process sherlog | Stop-Process` on Windows), and SHALL state that after that one kill, future upgrades self-heal.

#### Scenario: Pre-endpoint daemon yields instructions, not silence
- **WHEN** the MCP (new version) finds a healthy daemon that 404s /api/shutdown
- **THEN** the tool call fails with the version pair and the exact manual kill command, rather than silently proceeding against the stale daemon
