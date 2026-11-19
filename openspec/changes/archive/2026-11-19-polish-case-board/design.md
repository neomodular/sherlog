# Design: polish-case-board

## Context

The Case Board (vanilla JS, embedded) currently renders hypotheses/probes with raw store IDs and plain text. Dogfooding showed: duplicated labels ("h1 h1: ..."), no visual differentiation between suspects, and the confirmed root cause — the product's climax — rendered as an afterthought.

## Goals / Non-Goals

**Goals:**
- Every hypothesis/probe reference is a human name ("Hypothesis 1"), colored consistently, everywhere.
- The confirmed verdict is unmissable and reads as structured facts.
- Stay inside the existing constraints: vanilla JS, no frameworks, embedded assets, read-only.

**Non-Goals:**
- Changing stored data shapes (IDs stay `h1`/`p2` in store/API/MCP — display naming is presentation).
- Re-theming the terminal banner (brand.md's two-color terminal rule stands; colors are a board affordance).
- Charts/animations.

## Decisions

### D1: Display names derived in one UI helper
`displayName(id)` maps `h<N>`→"Hypothesis N", `p<N>`→"Probe N"; unknown shapes pass through unchanged. Used by every view (board, probes table, runs, diff, verdict panel, evidence tail). Raw IDs never render next to derived names. Root fix for "h1 h1:": the skill stores statements without ID prefixes (D5), and the UI strips a leading `^[hp]\d+\s*[:\-–]\s*` from legacy statements defensively at render time.

### D2: Hypothesis palette — fixed, colorblind-safe, deterministic
Six-color categorical palette chosen for distinguishability under common color-vision deficiencies (Okabe–Ito derived, tuned to the board's dark theme): assigned by hypothesis index (`h1`→color 1, ... cycling past 6). Confirmed state uses the brand coral accent regardless of palette color (the verdict owns coral); killed hypotheses desaturate to muted gray with a struck status label. Color appears as a left-edge chip/bar on hypothesis cards and as a small dot+name chip wherever a hypothesis is referenced (probes table, evidence, diff).

### D3: Verdict panel
When the board contains a confirmed hypothesis (and/or session resolution exists), case detail renders a panel ABOVE the board:
- Headline: the confirmed hypothesis statement (coral accent, largest text on the page)
- Fact rows (label/value grid): **Root cause**, **Fix**, **Confirmed by** (probe display-names + run references as chips), **Closed** (when resolved)
- Tone: calm, spacious, no decoration beyond the coral accent — the facts are the design.
Killed hypotheses collapse to a muted "ruled out" list under the active board. Open cases without a confirmation render exactly as today (no empty panel).

### D4: Tagline placement
"Elementary, dear developer." renders beside/below the sherlog wordmark in the board header (muted, small caps or italic — subordinate to the wordmark), and as the README hero strapline. Recorded in docs/brand.md as the product phrase with usage note (it is the *product* phrase; the "elementary." *moment* in the skill remains reserved for confirmed root causes).

### D5: Skill stores clean statements
SKILL.md instructs: hypothesis statements and evidence notes must not begin with their own ID ("h1: ..."); reference *other* entities by plain id when needed ("p3 showed...") — the UI upgrades those references via displayName where it renders them.

## Risks / Trade-offs

- [Legacy sessions with "h1: ..."-prefixed statements] → render-time defensive strip (D1); stored data untouched.
- [Palette clash with navy/coral brand] → palette tuned against the dark theme; coral reserved for confirmed state so the brand accent keeps one meaning.
- [More than 6 active hypotheses] → colors cycle; the board sorts active-first so collisions are rare in practice and chips always pair color WITH name, never color alone.

## Migration Plan

Presentation-only; no storage/API change. Rollback = revert.

## Open Questions

None.
