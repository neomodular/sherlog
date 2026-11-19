# Proposal: polish-case-board

## Why

Dogfooding surfaced readability problems in the Case Board: hypothesis labels render redundantly ("h1 h1: ..."), hypotheses and probes are visually undifferentiated (raw IDs like `h1`/`p2` in plain text), and a confirmed root cause — the payoff moment of the entire product — displays as an unstyled text line under the hypothesis. The board needs to read like a designed product, not a JSON dump.

## What Changes

- **Clean naming**: the UI displays "Hypothesis 1" / "Probe 1" derived from IDs (`h1`→"Hypothesis 1", `p2`→"Probe 2"), never raw IDs next to themselves. The skill is instructed to store statements/notes *without* ID prefixes so labels never duplicate.
- **Hypothesis colors**: each hypothesis gets a deterministic color from a colorblind-safe categorical palette (by index, cycling), shown as a colored chip/accent on its board card and reused everywhere the hypothesis is referenced.
- **Probes table**: columns read "Probe" / "Hypothesis" with full names ("Probe 1", "Hypothesis 2") and the hypothesis cell carries its color chip — linking instrumentation to suspect at a glance.
- **Tagline**: "Elementary, dear developer." appears beside the sherlog wordmark in the Case Board header (and the README hero), recorded in `docs/brand.md` as the product phrase.
- **Verdict panel**: when a hypothesis is confirmed, the case detail leads with a designed root-cause panel — visually distinct (confirmed color, generous spacing): the confirmed hypothesis statement as headline, root cause and fix summary as labeled facts, and the supporting evidence (probe + run references) presented as clear fact rows. Killed hypotheses visibly recede (muted, struck status), so the story of the investigation reads top-down: verdict first, surviving context after.

## Capabilities

### New Capabilities

(none)

### Modified Capabilities

- `case-board-ui`: ADDED — display-name derivation, hypothesis color system, probes table naming/colors, header tagline, confirmed-verdict panel, killed-hypothesis treatment.
- `debug-skill`: ADDED — statements/notes stored without ID prefixes (display naming is the UI's job).

## Impact

- Code: `internal/daemon/ui/` (most of the change — CSS + JS rendering), `skills/debug/SKILL.md` (no-prefix rule), `docs/brand.md` (tagline + palette), README hero line.
- No storage, API, or MCP schema changes — purely presentation plus one skill-authoring rule.
