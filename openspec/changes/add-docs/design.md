# Design: add-docs

## Context

Single source of truth today is README.md plus the openspec artifacts (which describe intent, not current behavior, and won't be what users read). Docs must serve two audiences: users (install, probe contract, troubleshooting) and integrators/contributors (architecture, tool reference).

## Goals / Non-Goals

**Goals:**
- A user can answer "what exactly does a probe line look like in my language" and "why am I seeing zero events" without reading Go code.
- Every MCP tool and config key shipped has a reference entry, kept current by review convention.
- README becomes shorter, not longer.

**Non-Goals:**
- GitHub Pages / site generator (deferred; plain Markdown must stand alone).
- Auto-generated docs from code (tool count is small; hand-written stays higher quality).
- Tutorials beyond the one dogfood walkthrough that already exists in examples/.

## Decisions

### D1: Plain Markdown in docs/, no generator
Six focused pages over one mega-page; relative links; renders on GitHub natively. Page set: `probe-contract.md`, `tools-reference.md`, `troubleshooting.md`, `architecture.md`, `configuration.md`, `brand.md`.

### D2: Accuracy contract over tooling
No doc-lint automation now; instead a one-line convention in README's contributing note: "changing a tool's schema or a config key requires updating its reference page in the same PR." Cheap, human, revisit if it fails.

### D3: configuration.md tracks add-config
Written from the add-config spec; if add-docs lands first, the page ships with a "requires sherlog >= version with config support" banner. Avoids cross-change blocking either way.

### D4: architecture.md inherits the design diagrams
The app→daemon→MCP→Claude diagram, storage layout, await/debounce semantics, and flood-control behavior move from openspec design docs into living documentation, rewritten in present tense as current behavior (openspec stays the historical record of why).

## Risks / Trade-offs

- [Docs drift from behavior] → D2 convention + troubleshooting page structured around observable symptoms (less likely to rot than internals prose).
- [Duplication between README and docs] → README keeps only install + 60-second tour; everything else becomes links.

## Migration Plan

Pure addition; no rollback concerns.

## Open Questions

None.
