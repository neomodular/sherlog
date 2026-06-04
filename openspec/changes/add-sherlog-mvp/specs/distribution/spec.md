# distribution

## ADDED Requirements

### Requirement: Single static binary builds
The project SHALL build `sherlog` as a dependency-free static binary (pure Go, no CGO) for darwin/arm64, darwin/amd64, linux/arm64, and linux/amd64 via goreleaser.

#### Scenario: Release build
- **WHEN** a version tag is pushed and the release pipeline runs
- **THEN** goreleaser produces archives for all four platform targets from a single configuration

### Requirement: Homebrew installation
The release pipeline SHALL publish a Homebrew formula to a tap so users install with `brew install <org>/tap/sherlog`, and `sherlog --version` SHALL report the released version.

#### Scenario: Fresh install via brew
- **WHEN** a user runs `brew install <org>/tap/sherlog` followed by `sherlog --version`
- **THEN** the binary installs and reports the released version

### Requirement: Claude Code plugin package
The repository SHALL contain a valid Claude Code plugin (`.claude-plugin/plugin.json`, `.mcp.json` launching `sherlog mcp`, and the `/debug` skill) installable via the plugin marketplace flow, with installation docs covering the brew prerequisite.

#### Scenario: Plugin install on a machine with the binary
- **WHEN** a user installs the sherlog plugin and `sherlog` is on PATH
- **THEN** Claude Code connects the MCP server and `/debug` is available with no further configuration

#### Scenario: Plugin install without the binary
- **WHEN** the plugin's MCP server fails to launch because `sherlog` is not installed
- **THEN** documentation and the plugin description direct the user to the brew install command

### Requirement: Version compatibility reporting
The MCP server SHALL include binary version in its server info and the daemon SHALL report version on `/health`, so mismatched plugin/binary combinations are diagnosable.

#### Scenario: Diagnosing a version mismatch
- **WHEN** a user reports tool errors after a plugin update
- **THEN** `sherlog --version` and the `/health` endpoint provide the versions needed to identify the mismatch
