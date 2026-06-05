# Tasks: polish-case-board

## 1. UI naming + colors

- [x] 1.1 `displayName(id)` helper (h/p derivation, passthrough otherwise) + render-time legacy self-prefix strip; adopt in every view (board, probes, runs, diff, evidence, verdict)
- [x] 1.2 Hypothesis palette (6 colorblind-safe colors tuned to the dark theme, index-assigned, cycling) with chips on board cards and beside every hypothesis reference; coral reserved for confirmed, muted+struck for killed
- [x] 1.3 Probes table: "Probe"/"Hypothesis" headers, full display names, hypothesis color chip in cell

## 2. Verdict panel + tagline

- [x] 2.1 Verdict panel on case detail (confirmed statement headline, Root cause / Fix / Confirmed by / Closed fact rows with probe+run chips); killed hypotheses collapse to muted ruled-out list; no empty panel on open cases
- [x] 2.2 Header tagline "Elementary, dear developer." beside the wordmark, subordinate styling
- [x] 2.3 Asset tests updated (still embedded, no external origins, GET-only)

## 3. Skill + docs

- [x] 3.1 SKILL.md: no self-ID prefixes in statements/notes rule
- [x] 3.2 docs/brand.md: tagline + usage boundary + hypothesis palette/state rules; README hero strapline
