# Brand

sherlog has a small, deliberate identity: a detective metaphor, one mascot, two
colors, and a fixed vocabulary. The brand is functional — it makes a sherlog
probe recognizable in a diff and a sherlog session recognizable in a terminal —
so the rules below are constraints, not decoration.

## The metaphor

You (Claude) are **the detective**. The sherlog daemon is **Watson**: it watches
port 2218, records evidence, and holds the case board. The investigation is a
*case*; suspects are *hypotheses*; evidence is *probe output*. The daemon board,
not conversation memory, is the single source of truth.

## Mascot

Watson — a coral detective in a navy inspector cap. The **canonical mascot is the
raster image**, [`mascot.png`](mascot.png): the square badge logo in the Case Board
header, and the source of the brand's two colors. The wide hero — Watson magnifying
the glowing red lines out of a wall of logs — is [`banner.jpeg`](banner.jpeg); it
heads the README and doubles as the GitHub social-preview image.

Terminals can't render the mascot's soft, rounded shape, so **the terminal does not
draw the mascot** — it prints a text wordmark banner instead (see below). There is
no ASCII sprite to keep in sync. *(Restoring a terminal sprite later is possible,
but it would be a deliberately stylized icon, not a faithful copy of the image.)*

## Colors

Two colors — navy and coral — applied with ANSI truecolor in the terminal and as
CSS variables in the Case Board:

| Role | Color | Truecolor (SGR) | Applies to |
|---|---|---|---|
| Cap | **navy** | `38;2;30;58;110` | the inspector cap; the Case Board topbar |
| Body | **coral** | `38;2;255;111;97` | the wordmark accent; the confirmed verdict |

Color in the terminal is governed by the `color` config key: `auto` colorizes only
when the terminal supports truecolor, `always` always emits the sequences, `never`
strips all escapes and prints the plain wordmark. See
[configuration.md](configuration.md).

## Wordmark banner

In `detective` verbosity the skill prints a small text banner at session start and
at major transitions — the wordmark with the tagline, then the case status line:

```
sherlog · Elementary, dear developer.
case "<title>" · #<id> · N suspects · M probes · watching :2218
Case Board: http://127.0.0.1:2218 — watch the investigation live
```

- Line 1 is the **wordmark + tagline** — constant; it identifies the product.
- `<title>` is the case title; `<id>` is the session ID; `N` is active suspects on
  the board; `M` is registered probes not yet removed; the port is the daemon's
  actual port (honor `SHERLOG_PORT`).
- The Case Board link appears **once**, in the opening banner only — not at later
  transitions.

Colorized (truecolor): the wordmark `sherlog` in **coral** (`38;2;255;111;97`,
bold) and the tagline dimmed; `color: never` prints the plain banner with no
escapes.

## Vocabulary

Three exact phrases mark three transitions, and **nothing else** does:

| Phrase | When |
|---|---|
| **"the game is afoot"** | entering `await_run` — awaiting the reproduction |
| **"elementary."** | only when the root cause is confirmed by probe evidence |
| **"case closed"** | only after the cleanup grep returns zero matches |

These are earned, not garnish: "elementary." requires confirming evidence, and
"case closed" requires a clean cleanup grep. Do not use them at other moments.

## Tagline

**"Elementary, dear developer."** is the product tagline. It appears beside the
sherlog wordmark in the Case Board header (subordinate to the wordmark — muted,
italic), in the terminal wordmark banner, and as the README hero strapline. It
identifies the product; it is *not* a transition phrase.

Keep it distinct from the **"elementary."** *moment* in the vocabulary table
above: that single earned word stays reserved for a confirmed root cause and is
never printed elsewhere. The tagline is the always-on product phrase; the
"elementary." moment is the once-per-case payoff. Do not blur the two.

## Hypothesis palette (Case Board)

The Case Board colors each hypothesis from a fixed, colorblind-safe categorical
palette so suspects are distinguishable at a glance and a color follows its
hypothesis across every view (board card, probes table, evidence, verdict). This
is a **board affordance only** — it does not touch the two-color brand palette
above.

| # | Color | Hex |
|---|---|---|
| 1 | blue | `#0072b2` |
| 2 | bluish green | `#009e73` |
| 3 | reddish purple | `#cc79a7` |
| 4 | sky blue | `#56b4e9` |
| 5 | vermillion | `#d55e00` |
| 6 | muted violet | `#9467bd` |

Rules:

- **Assigned by index, cycling.** Hypothesis 1 → color 1, … Hypothesis 7 → color 1
  again. Colors are derived from the Okabe–Ito colorblind-safe set, tuned to the
  board's light surface.
- **Color always pairs with the name.** A color chip never carries meaning alone —
  it always sits beside "Hypothesis N".
- **Coral = confirmed, only.** A confirmed hypothesis takes the brand coral accent
  regardless of its palette color; the verdict owns coral, so coral is absent from
  the palette above.
- **Muted = ruled out.** A killed hypothesis desaturates to muted gray with a
  visible "ruled out" status and recedes below the active board.

## Usage rules

- **Detective vs. minimal.** The full presentation (wordmark banner, status line,
  vocabulary) is the `detective` verbosity. In `minimal` verbosity, drop the
  wordmark and the phrases entirely and use plain status lines — but keep every
  functional fact (status, Case Board link if shown, cleanup result, grep outcome,
  verdict prompts, zero-event guidance). **Minimal removes theater, not
  information.**
- **The banner is text.** State is conveyed by the status line text — there is no
  mascot art in the terminal to alter.
- **The port is the brand.** 2218 is 221B Baker Street, Sherlock Holmes's address.
  The fixed port is what makes a sherlog probe instantly recognizable in a diff —
  see [probe-contract.md](probe-contract.md) on greppability.
- **Read-only Case Board.** The browser UI only ever issues GET requests; all
  mutation goes through the MCP tools.
