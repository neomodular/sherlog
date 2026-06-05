# Contributing to sherlog

Thank you for your interest in contributing to sherlog. This guide covers everything you need to get started.

## Prerequisites

- **Go** >= 1.26 (the only build requirement — pure Go, no CGO)
- **Claude Code** (for testing the plugin and `/debug` skill end to end)
- Familiarity with the [architecture](docs/architecture.md) helps for daemon/store work

## Development Setup

```sh
# Clone and build
git clone https://github.com/neomodular/sherlog.git
cd sherlog
go build ./...

# Run the full test suite
go test ./...

# Vet and format checks
go vet ./...
gofmt -l .        # must print nothing

# Install your working copy onto PATH
go install ./cmd/sherlog
```

To test the plugin against your working copy: `/plugin marketplace add <path-to-your-clone>` in Claude Code, install the `sherlog` plugin, and ensure your freshly built binary is first on PATH. Kill any running daemon (`sherlog` process) after rebuilding so the new binary auto-respawns.

## Project Structure

```
cmd/sherlog/       # CLI entry: daemon / mcp / probes / config / notes subcommands
internal/
  store/           # Sessions, hypotheses, probes, runs, events; flood control;
                   #   adoption-on-open; persistence (state.json + logs.jsonl); pub/sub
  daemon/          # HTTP server: ingest, /health, internal API, SSE, stats
    ui/            # Case Board — embedded static assets (go:embed, vanilla JS)
  mcp/             # MCP stdio server and tool implementations
  config/          # Config file schema, precedence (env > file > default), validation
  notes/           # Field-notes channel (agent observations, maintainer inbox)
skills/debug/      # The /debug skill (detective loop) — SKILL.md
.claude-plugin/    # Plugin + marketplace manifests
docs/              # Reference documentation (kept accurate against the code)
examples/          # Seeded-bug sample apps for dogfooding
openspec/          # Change proposals, designs, and specs (planning history)
```

### Core Pipeline

```
probe (one-line HTTP POST) --> daemon ingest --> store (runs, flood control,
adoption) --> await/query/diff --> MCP tools --> /debug skill --> fix --> cleanup
```

The Case Board UI is strictly read-only (GET only); all mutation goes through the MCP tools.

## Planning with OpenSpec

Substantial changes (new capabilities, behavior changes) are planned as OpenSpec changes under `openspec/changes/` — proposal, design, delta specs, and tasks. Look at merged changes there for the house style. Small fixes don't need this ceremony; a focused PR with a regression test is enough.

## Testing Guidelines

- All changes must include tests; bug fixes need a regression test.
- Store tests use an injectable root directory (never write to the real `~/.sherlog`).
- Daemon tests use `httptest` or an ephemeral listener — never assume port 2218 is free.
- Table-driven tests are the house style; match the existing files.
- CI runs `go test -race ./...` on Linux; local Windows machines without a GNU toolchain can't run `-race` — that's expected, CI is authoritative for races.

## Code Style

- `gofmt` formatting (CI enforces `gofmt -l .` clean)
- `go vet ./...` clean
- Standard library only, plus the official MCP Go SDK — new dependencies need a strong case
- Comments: concise godoc on exported identifiers; inline comments explain *why*, not *what*
- Errors wrapped with context (`fmt.Errorf("...: %w", err)`), never swallowed silently

## Documentation Convention

If your PR changes an MCP tool's schema, a config key, or a CLI flag, update the matching reference page in `docs/` **in the same PR** (`docs/tools-reference.md`, `docs/configuration.md`, etc.). Docs describe the binary as it is, not as planned.

## Commit Conventions

This project uses [Conventional Commits](https://www.conventionalcommits.org/):

```
<type>(<optional scope>): <description>
```

Types: `feat`, `fix`, `docs`, `test`, `refactor`, `chore`, `perf`, `ci`.

Examples from the history:

```
feat(store): resolutions, keyword recall, run diff, event pub/sub
fix(store): merge flood buffers on adoption replay instead of overwriting
docs: explicit Claude Code plugin install commands in README
```

## Pull Request Process

1. **Fork** the repository and create a branch from `main` (`feat/diff-export`, `fix/sse-reconnect`, ...).
2. **Make your changes** with tests covering the new behavior.
3. **Run the full validation suite**: `go build ./... && go vet ./... && go test ./... && gofmt -l .`
4. **Commit** using conventional commit messages.
5. **Open a Pull Request** against `main` using the PR template.

### PR Expectations

- Every PR must pass CI (build, vet, race-enabled tests, gofmt).
- Keep PRs focused — one logical change per PR.
- Breaking changes (tool schemas, config keys, probe contract, storage format) must be called out explicitly.

## Security Conventions

These are invariants — PRs that weaken them will be declined:

- The daemon binds to `127.0.0.1` only.
- Session IDs are unguessable; events for unknown sessions are silently dropped.
- The Case Board issues GET requests only and embeds all assets (no external origins).
- All data stays local under `~/.sherlog/`; nothing is ever uploaded.

See [SECURITY.md](SECURITY.md) for reporting vulnerabilities.

## Questions?

Open an issue — happy to help you find the right place to contribute.
