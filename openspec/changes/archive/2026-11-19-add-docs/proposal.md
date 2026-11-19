# Proposal: add-docs

## Why

The README is a good trailer but sherlog has no manual: the probe contract exists only as scattered examples, the 11 MCP tools have no reference, and predictable failure modes (port conflict, zero events, binary not on PATH) have no troubleshooting page. As v0.2 features land (Case Board, config), undocumented surface compounds.

## What Changes

- New in-repo `docs/` folder: probe contract reference, MCP tool reference, troubleshooting guide, architecture overview, configuration reference, and a brand page (mascot, sprite, vocabulary, colors).
- README slims to the trailer role and links into docs/ for depth.
- Docs are versioned with the code in the same repo/PRs; GitHub Pages publication is explicitly deferred.

## Capabilities

### New Capabilities

- `documentation`: The docs/ reference set — required pages, accuracy obligations (every shipped MCP tool and config key documented), and README linkage.

### Modified Capabilities

(none — documentation only; no runtime behavior changes)

## Impact

- New files under `docs/`; README edits; no code changes.
- Maintenance contract: tool/config changes must update their reference pages (checked in review, noted in CONTRIBUTING section of README).
- Coordination: configuration.md documents the add-config surface — write it against that change's spec, or land it after add-config merges.
