---
name: debug
description: Hypothesis-driven debugging via the sherlog daemon. Use when the user reports a bug, says "debug this", "/debug", "why does X fail", "track down", or asks to investigate flaky/intermittent behavior. Drives a detective loop — at least 3 suspects, one discriminating probe each, a blocking wait while the user reproduces, evidence-based elimination, fix, fix-check run, and guaranteed probe cleanup. "/debug resume" continues a prior investigation.
---

# /debug — the detective loop

You are the detective. The sherlog daemon is Watson: it watches port 2218,
records evidence, and holds the case board. **The daemon board is the single
source of truth — never reason from conversation memory about which suspects are
alive, where probes are, or what runs found.** After `/clear`, compaction, a
crash, or days later, the board is what survives. Read it; do not remember it.

All state lives behind MCP tools (`debug_start`, `set_hypotheses`,
`register_probe`, `await_run`, `close_run`, `query_logs`, `diff_runs`,
`update_hypothesis`, `remove_probe`, `debug_end`, `debug_resume`). Pass the
`session_id` from `debug_start` to every subsequent call. One more tool stands
apart from the case board — `report_observation`, the silent channel for sherlog's
own misbehavior (see "When sherlog itself misbehaves" below).

## The loop at a glance

```
gather context → debug_start → ≥3 suspects (set_hypotheses)
→ ≥1 discriminating probe per suspect, each with its prediction pair (register_probe)
→ print banner → "the game is afoot" → user reproduces → await_run → close_run(verdict)
→ analyze summary → kill / refine / split suspects (update_hypothesis, cite probe+run)
→ iterate until one confirmed
→ map_blast_radius (before the fix) → annotate every hit honestly
→ fix → await_run(prediction=…) → fixed-check run → "elementary."
→ surface any sibling-bug hits → debug_end → remove every probe → grep = 0 matches → "case closed"
```

The daemon now enforces the loop's shape as well as its evidence: it rejects a
board under three suspects, a kill/confirm with no cited probe+run, a confirm
without a reproduced run or predictions on the citing probe, a fixed-check close
with no recorded prediction, a probe at a file/line that does not exist, a solved
close whose confirmed hypothesis is not confirmed on the board, and a blast-radius
search mapped before a confirm or whose pattern misses the confirmed culprit's own
file. **A rejection is a discipline breach to repair, never an error to route
around** — see "When the daemon rejects a transition" below.

---

## 1 · Open the case

1. **Gather bug context first.** Get the symptom, how to reproduce it, and which
   files/area are involved. If the report is vague ("login is broken"), ask one
   or two sharp questions before starting — a good investigation needs a
   reproducible symptom.
2. **Author a title and a structured description** (rules below), then call
   `debug_start(title, bug_description)`. It returns:
   - `session_id` — thread it through every later call.
   - `title` — the case identity echoed back (the one you supplied, or a derived
     fallback if you omitted it). Use it in the banner and whenever you name the case.
   - `probe_contract` — the `url_template` (`http://127.0.0.1:2218/log/<session>/<probe>`),
     a one-line `note`, and `one_liners` per language (js, python, go, ruby, curl).
   - `preferences` — `{verbosity, color}` for presentation (see "Presentation
     preferences" below). Apply them to every line you print this session.
   - `warn_same_cwd` — if non-null, **another open session already exists for this
     directory.** Warn the user (do not block): "There's already an open sherlog
     case (#<id>) for this folder — continuing as a separate investigation. Run
     `/debug resume` instead if you meant to pick that one up." Then proceed.
   - `related_cases` — possibly-related **solved** past cases the daemon recalled
     from this bug's description (each with `session_id`, the old `description`,
     `root_cause`, and `fix_summary`). They are **leads, not evidence** — use them
     per "Recalled cases as leads" below.

### Authoring the title and description (binding)

`debug_start` takes a **title** and a **bug_description**. You write both.

**Title** — the case identity shown everywhere a case is referenced (Case Board
list, banner, recall results, resume). Make it:

- A short, specific summary of the *failure*, **≤ 60 characters**.
- Imperative or noun-phrase, naming the observed problem — not the whole story.

> ✅ `Login 401 after idle timeout`
> ✅ `Cart total off by a cent on discounts`
> ✅ `Race between token refresh and request`
> ❌ `Bug in auth` (vague — which bug?)
> ❌ `The login endpoint sometimes returns a 401 error after the user has been
>    idle for a while and the token expires` (that's the description, not a title)

If you genuinely cannot name the failure yet (the symptom is still unclear), ask
your one clarifying question first (below), then title it. The daemon will derive a
truncated fallback if you omit the title, but a derived paragraph-stub is worse than
a real title — **always supply one.**

**Description** — the detailed narrative, written as plain text under *soft-
structured* headings. Include only the headings you have **real content** for:

```
Symptom: 401 on the first request after the tab sits idle ~5 min. Exact error:
  "token_expired" from /api/me.
Expected: silent token refresh keeps the session alive; no 401.
Repro: log in, leave the tab idle 5+ min, click anything that calls the API.
Context: started after the 2.3 auth refactor; only Safari reported so far.
```

Binding rules for the description:

- **Quote exact error text** in `Symptom:` when you have it — it is the highest-
  signal recall token.
- **Never invent** an `Expected:` or `Repro:` the user did not state. Omit a heading
  rather than fabricate its content — a missing heading is honest; a made-up one
  misleads the next investigator.
- **One clarifying question, max.** Ask **only** when the *symptom or expected
  behavior is genuinely unclear* (you cannot tell what actually goes wrong).
  Otherwise proceed with what you have — partial structure is fine.
- Headings are plain text, not a schema: the Case Board bolds the `Symptom:` /
  `Expected:` / `Repro:` / `Context:` labels on render, but storage is one string.

### Recalled cases as leads (never evidence)

When `related_cases` is non-empty, read them before naming suspects. A prior root
cause that plausibly fits the new symptom is a strong *lead*: turn it into one of
your hypotheses and **cite the source case** in that hypothesis's statement —
e.g. `set_hypotheses(..., ["float rounding in discount calc (similar to case
#b2c1)", ...])`. Binding limits:

- **Leads only.** A recalled case may *suggest* a suspect; it may **never kill or
  confirm** one. Probes remain the only evidence — every kill/confirm still needs a
  probe + run note, exactly as for any other suspect.
- **No fabrication.** Cite a case only when you actually used it. If nothing recalled
  fits, ignore `related_cases` entirely and form suspects from the symptom.
- Recall is keyword-matched and can mislead; treat a match as "worth a probe", not
  "the answer". You may mention to the user that a similar case was solved before.

## 2 · Name the suspects (≥3)

Form **at least three distinct hypotheses** for the root cause, then commit them
with `set_hypotheses(session_id, hypotheses=[...])`. They come back as `h1, h2,
h3, …`, all `active`.

Make them genuinely different mechanisms, not three flavours of one guess. For
"login fails intermittently": h1 race between token refresh and request; h2 stale
session cache; h3 connection-pool exhaustion under load. Breadth here is what
makes the evidence decisive later.

**Store statements as bare claims — no self-ID prefix.** Write the hypothesis
statement (and every evidence note) *without* leading with its own identifier:
`"race between token refresh and request"`, never `"h1: race between token
refresh and request"`. Display naming is the Case Board's job — it renders
"Hypothesis 1" from the id and would otherwise show a duplicated label. Referring
to *another* entity inside a note is fine and encouraged (`"p3 fired only in run
r2"`); the UI upgrades those references to display names where it shows them.

## 3 · Plant discriminating probes (≥1 per suspect)

For **every** hypothesis, place at least one probe whose output *distinguishes
that suspect from its rivals* — not a mere "execution reached here" marker. A
probe is discriminating when its payload would look different depending on which
hypothesis is true.

> h1 (race) vs h2 (stale cache): one probe posting
> `{token, token_age_ms, cache_age_ms, t}` settles both — a null/expired token
> with a fresh cache points at the race; a populated token with a stale cache
> points at the cache. One line, two suspects discriminated.

**Probe rules (binding):**

- **One fire-and-forget line.** Take the form from `probe_contract.one_liners`
  for the language. Prefer whatever HTTP facility the codebase already uses.
- **Never await it, never let it throw.** The JS form ends in `.catch(() => {})`;
  Go runs it in a goroutine; Python/Ruby swallow exceptions; curl backgrounds and
  silences. The probe must never block or break the host app.
- **Never set a JSON `Content-Type`.** Bodies go as default `text/plain` so
  browser probes stay CORS "simple requests" with no preflight. The daemon parses
  the body as JSON anyway and falls back to a raw string — a probe can't fail
  validation. (If you ever hand-write a probe, do not add headers.)
- **No new imports or wrappers** where the language allows a bare call. Put the
  discriminating values directly in the body: `JSON.stringify({token, age, t})`.
- Use a distinct probe ID per location: `p1, p2, p3, …`. Substitute it for
  `<probe>` in the URL template.

### Author the prediction pair (binding)

A discriminating probe is only discriminating if you can say, **before** the run,
what its payload looks like under each outcome. Register that as a pair:
`expected_if_true` (what the probe shows if *its* hypothesis is the culprit) and
`expected_if_false` (what it shows if that hypothesis is innocent). The daemon
validates the pair — **both or neither**, and the two must **differ** (a pair that
reads the same under either outcome proves nothing and is rejected).

- **Every hypothesis SHALL have at least one predicted probe before the wait
  begins.** A plain path-tracer probe ("did we reach this branch") MAY omit the
  pair, but a suspect whose only probe is unpredicted cannot later be *confirmed* —
  the confirm gate only accepts a citation to a probe that carries predictions.
- Write concrete payload descriptions, not restatements of the hypothesis:
  `expected_if_true="token=null, token_age_ms past TTL while the request fired"`,
  `expected_if_false="token populated, token_age_ms well under TTL"`.

After editing each probe into the code, **register it** with its file, line,
hypothesis, and — for a discriminating probe — its prediction pair:

```
register_probe(session_id, id="p1", file="src/auth.js", line=42,
  hypothesis_id="h1",
  expected_if_true="token=null, token_age_ms past TTL while request fired",
  expected_if_false="token populated, token_age_ms under TTL",
  note="posts token + cache age to split race vs stale")
```

Registration is the cleanup guarantee — an unregistered probe is an orphan. The
daemon also **verifies the location**: it resolves `file` against the session cwd
(absolute paths as-is) and rejects a file that does not exist or a `line` past the
file's end, with an error naming the resolved path. If you hit that, you named the
wrong path (register the *source* file relative to the session cwd, not a
bundled/generated one) or the wrong line — fix the argument and re-register; never
invent a location the cleanup grep can never find.

## 4 · The game is afoot — reproduce and wait

1. Print the banner (section "Branded presentation"), then say **"the game is
   afoot"** and ask the user to reproduce the bug now. If probes were added to a
   compiled or bundled app, remind them to **rebuild/restart** so the new lines
   run.
2. Call `await_run(session_id)` (default 120s). It opens a run, blocks until probe
   activity goes quiet (~2s debounce) after first firing, or returns at timeout.
   **You suspend here — do not ask the user to "type done".** The result has:
   - `run` (with its `id`), `reason` (`quiet` | `timeout` | `deadline`),
     `total_seen`, and `summary`: one entry per registered probe with
     `total` (true count, `0` if it never fired), `adopted` (how many of `total`
     were attributed by pre-run adoption — see below), `truncated`, and sampled
     `events`.
3. **Slow reproduction?** If `reason` is `timeout`/`deadline` and the user is
   still working, just call `await_run(session_id)` again — it re-attaches to the
   same open run. Repeat as needed.
4. When the user has finished the attempt, ask for the **verdict** and record it:
   `close_run(session_id, verdict=...)` — `reproduced`, `not-reproduced`, or
   (later) `fixed-check`. Always ask; never assume the outcome.

### Zero-event guard (do this before blaming any suspect)

If `await_run` returns with `total_seen == 0` / every probe `total: 0` **but the
user says they reproduced the bug**, the problem is almost certainly the wiring,
not the hypotheses. Do **not** kill suspects. Check, in order:

1. **Daemon connectivity** — is the daemon answering? `curl -s
   http://127.0.0.1:2218/health` should return JSON with a `version`. No
   response → the daemon isn't running or the MCP server couldn't spawn it;
   suggest restarting the MCP server / re-invoking a tool to trigger auto-spawn,
   and (if `SHERLOG_PORT` is set) curl that port instead.
2. **Probe execution** — did the app actually run the new lines? A bundled/compiled
   app needs a **rebuild/restart**; the code path may not have been hit; the probe
   line may be after an early return/throw.
3. Only once probes demonstrably fire do run results speak to the hypotheses.

### Adopted evidence (fast reproductions that beat `await_run`)

A scripted repro can finish *before* `await_run` opens the run. The daemon adopts
those just-fired events into the run anyway, so they are not lost — and discloses
it: a probe's `adopted` count is how many of its `total` events were attributed by
inference (timestamp + run boundary) rather than seen live during the wait.
`adopted == total` for a probe means **every** one of its events was inferred; a
run whose probes are all fully adopted is an entirely inferred attribution.

Treat adopted evidence as **valid but labeled** — never silently discount it,
never blindly trust it:

- **Normal runs**: adopted counts are just provenance. Read the summary as usual.
- **Fully adopted run + a verdict that carries weight** (above all a
  `fixed-check`): sanity-check before concluding. Are the probes you *expected*
  this reproduction to hit actually present, and are their values plausible for
  what you predicted? If yes, accept it and **note that attribution was
  inferred** when you state the conclusion.
- **Anything inconsistent** (a discriminating probe you expected is absent, or a
  value contradicts the prediction): do **not** conclude on inferred evidence.
  Ask the user to **reproduce once more while the run is open** (`await_run` is
  already waiting, or call it again), then read the live result.

## 5 · Read the evidence; kill, refine, split

Inspect the per-probe summary (and `query_logs(session_id, probe=..., run=...)`
for detail — counts plus first/last samples, truncation always disclosed). Then
act on the board. **A kill or confirm SHALL cite the probe and run structurally**
— pass `probe_id` and `run_id` (the probe and the *closed* run whose evidence you
are reasoning from) alongside the free-text note. The note stays the
human-readable explanation of the same evidence; the citation is what the daemon
cross-checks against its own registry. A refine (`active`) needs no citation.

- **Kill** a suspect the evidence refutes — cite the probe+run:
  `update_hypothesis(session_id, "h2", "killed", probe_id="p4", run_id="r2",
  note="p4 in r2 shows cache_age_ms=12 — cache was fresh, not stale")`. A probe
  that fired **zero times** (`total: 0`) is a valid citation — "fired zero times"
  is load-bearing evidence.
- **Confirm** the one the evidence proves — cite a *predicted* probe and a run,
  and only after the bug has reproduced under instrumentation:
  `update_hypothesis(session_id, "h1", "confirmed", probe_id="p1", run_id="r2",
  note="p1 in r2: token=null, token_age_ms past TTL while request fired — the
  race")`. The daemon rejects the confirm unless the session has ≥1 run closed
  `reproduced` **and** the cited probe carries `expected_if_true`/`expected_if_false`.
- **Refine / split**: if evidence reshapes a suspect or reveals two mechanisms
  hiding under one, update its statement (status `active`, no citation) or call
  `set_hypotheses` again with the **full** board (replace semantics — resubmit the
  survivors plus the new suspect(s), still ≥3) and re-probe.

Iterate steps 3–5 — add or move probes, run again — until exactly one hypothesis
is `confirmed` by probe evidence. Do not declare a winner on a hunch.

### Determinism is reported from the computed rate, never asserted

`await_run` and `debug_resume` return a **repro rate** — `reproduced` over
(`reproduced` + `not-reproduced`) across the session's closed runs, with the raw
counts (`2/5`). State determinism *from that number* ("reproduced 2/5 runs — this
is intermittent"), never from memory ("it always fails"). For an intermittent
bug, a **single** `not-reproduced` run in which a discriminating probe stayed
quiet does **not** kill its suspect on its own — the bug simply didn't fire that
time. Keep gathering runs; kill only when the evidence, across the runs you have,
actually refutes the suspect.

## 6 · Map the blast radius — after confirm, before the fix

A confirmed root cause is rarely alone: the same anti-pattern usually lives at
sibling call sites. Before you touch the fix, hunt for those siblings — and let
the **daemon** run the hunt so the hit list is a recorded fact, not a claim. "I
grepped for other occurrences" is exactly the kind of unrecorded, unrunnable
assertion the rest of sherlog refuses to accept; `map_blast_radius` replaces it
with a search the daemon executes and stores.

1. **Author a pattern that targets the defect *mechanism*, not the symptom.** The
   regex should match the anti-pattern you just confirmed — the float-rounding
   call, the missing null-check shape, the unguarded cache read — **not** the error
   text the user saw. Symptom prose does not recur at sibling sites; the mechanism
   does.

   ```
   map_blast_radius(session_id, pattern="toFixed\\(2\\)\\s*\\*\\s*100",
     note="float cents rounding before the discount multiply")
   ```

   The daemon compiles the pattern (Go's RE2 engine — a pathological pattern cannot
   wedge it), walks the session cwd itself, and records every hit as `{file, line,
   excerpt}`. **You never supply, add, or remove a hit** — the whole point is that
   the list is the daemon's finding, not yours. It returns the hits, a `truncated`
   flag, and the `unreviewed_count`.

   One hygiene rule before you search: **never write scratch artifacts (saved tool
   output, notes, temp scripts) inside the debugged repo tree.** The walk covers
   the entire session cwd, so your own bookkeeping files become hits and pollute
   the radius. Keep scratch files outside the project — a temp directory.

2. **Map it while the anti-pattern still exists at the culprit — before the fix.**
   The daemon **rejects a pattern that does not match the confirmed culprit's own
   file** (the false-coverage gate): a pattern that misses the known bug proves
   nothing about siblings. There is **no override** — if you fix the culprit first,
   the pattern no longer matches it and the search is worthless. So the order is
   fixed: confirm → map the radius → *then* fix.

3. **Truncation is disclosed — narrow and re-run.** If `truncated` is true the
   search hit the cap: the pattern is too broad and is matching noise. Tighten it
   and call `map_blast_radius` again. A re-run **replaces** the whole radius and
   clears every annotation (they graded a different search), so never treat stale
   verdicts as still standing.

4. **Grade every hit honestly** with `annotate_blast_radius`. Read each site and
   assign a verdict:
   - `sibling-bug` — the same defect lives here.
   - `safe` — the pattern caught this line but it is not actually buggy. **A
     legitimate verdict**: most broad-pattern hits are false positives, and saying
     so is honest work, not a cop-out.
   - `already-covered` — the site is already fixed, tested, or guarded.

   ```
   annotate_blast_radius(session_id, annotations=[
     {file:"src/pricing.js", line:88, verdict:"sibling-bug",
       note:"same toFixed→*100 rounding, no integer-cents path"},
     {file:"src/tax.js", line:12, verdict:"safe",
       note:"operates on integer cents already; the match is inside a comment"},
   ])
   ```

   The daemon accepts a verdict **only for a `{file, line}` it recorded** — you
   cannot grade a site the search did not find. Partial grading is fine; every hit
   you leave alone stays `unreviewed`, and the result counts them.

5. **Say the unreviewed part out loud.** A hit you genuinely cannot judge —
   generated code, a vendored file, a construct you do not understand — **stays
   `unreviewed`; do not guess a verdict to make the number look clean.** Tell the
   user which sites you could not evaluate and why. An honest "5 hits: 1 sibling
   bug, 3 safe, 1 unreviewed (generated, couldn't assess)" beats a fabricated
   all-clear.

6. **Never claim coverage without a recorded radius.** If the user asks whether the
   bug exists elsewhere, answer **from the board's counts** — "the radius found 7
   hits: 2 sibling bugs, 4 safe, 1 unreviewed" — or run the search first. Never
   answer from recollection, and never restate coverage in prose to paper over a
   rejected pattern: if the gate rejects the pattern for missing the culprit,
   **refine the pattern and re-run**, do not narrate.

The radius is **optional evidence, not a close requirement** — it never gates
`debug_end` (see the cleanup gate below for how `sibling-bug` hits are surfaced at
close). A case resumed *after* the fix was already applied simply proceeds without
one: the sequencing gate makes a post-fix search worthless, and that is fine.

## 7 · Fix, then verify with a fixed-check run

1. Apply the fix for the confirmed hypothesis.
2. **Record the prediction on the run, not in the conversation.** Ask the user to
   retest and open the fixed-check run *with* your prediction:
   `await_run(session_id, prediction="p1's token now populated; error-path probe
   p5 fires zero times")`. The board — never conversation memory — holds the claim
   the fixed-check is judged against. The daemon stamps the prediction on the run
   at that call, before it returns any summary, and it is immutable once set.
3. Then `close_run(session_id, verdict="fixed-check")`. **`close_run(fixed-check)`
   is rejected if the run carries no prediction** — if you awaited without one,
   re-await with the `prediction` and have the user reproduce once more (do not
   restate the prediction in prose to satisfy the gate — put it through the tool).
4. Confirm the failure signature changed **as predicted** via the probe summary /
   `query_logs`, *and* the user reports the bug is gone. To make the before/after
   contrast explicit, `diff_runs(run_a=<reproduce run>, run_b=<fixed-check run>)`
   lists the probes that diverged between the failing and fixed runs (divergent
   ones first) and echoes the fixed-check run's recorded `prediction` — judge the
   divergence against that recorded claim, not against a remembered one. It is a
   fast confirmation that the discriminating probe stopped firing (or changed
   value) exactly where the fix should bite. Only with both signals: say
   **"elementary."** and go to cleanup. If the signature didn't change, the fix is
   wrong or the cause is misidentified — reopen the board.
   - If the fixed-check summary is **fully adopted** (the repro beat `await_run`),
     apply the adopted-evidence rule: accept it as verification only when the
     expected probes are present and values match the prediction (say so, noting
     the attribution was inferred); if anything is inconsistent, ask for one live
     reproduction before declaring the fix verified.

## 8 · Cleanup gate — case closed only when clean

The probe URL is its own marker, so leftover probes are always findable.

**Before you close, surface the sibling bugs.** If the radius holds any
`sibling-bug` hits, list every one to the user (file + line + your note) and ask
how they want to proceed — fix them now as part of this case, or track them
separately. **It is their call, and it does not block the close.** Never delay or
gate `debug_end` on an unmapped, partial, or unreviewed radius: the radius informs
the close, it never controls it.

1. `debug_end(session_id)` → `unremoved_probes` (each with `file` + `line`),
   `greppable_fragment` (`…/log/<session>/`), and `cleanup_complete`.
   **Record the resolution when solved:** if a root cause was confirmed, pass all
   three of `root_cause`, `fix_summary`, and `confirmed_hypothesis_id` to
   `debug_end` so the case becomes recall material for future investigations —
   `debug_end(session_id, root_cause="float rounding in discount calc",
   fix_summary="switched discount math to integer cents", confirmed_hypothesis_id="h1")`.
   Keep the fields concise and factual; the confirmed hypothesis is the one you
   marked `confirmed` on the board. The daemon enforces the contract: supplying any
   resolution field requires **all three**, and `confirmed_hypothesis_id` must name
   a hypothesis whose status is `confirmed` on the board — otherwise the close is
   **rejected and the session stays open** (it is *not* silently downgraded to
   unsolved). If you get that rejection, confirm the hypothesis with evidence first,
   or close unsolved deliberately. **Never fabricate a resolution:** if the case is
   closing **unsolved**, say so plainly to the user and call `debug_end(session_id)`
   with no resolution fields — an unsolved close is valid and must not invent a
   root cause.
   - **Prevention references, only when real.** If a regression test or guardrail
     *actually exists* at close time, record it: `regression_test_ref` (a test name
     that now covers the bug, e.g. `"TestRefreshRace"`) and/or `guardrail`
     (`{type, ref}`, `type` ∈ `test | lint | alert | doc`). Sherlog records and
     displays them; it never fetches or runs them. **Never invent one** — a solved
     close with no prevention artifact simply omits these fields. They ride
     alongside a full resolution; a lone reference is a partial resolution the
     daemon rejects.
2. **Remove every listed probe line** from the code. After deleting each,
   `remove_probe(session_id, id)`.
3. **Grep gate (mandatory):** search the repo for the session fragment and require
   **zero matches** before declaring the case closed:

   ```
   grep -rn "2218/log/<session-id>" .
   ```

   If `SHERLOG_PORT` was overridden, the URL carries that port — grep the actual
   `greppable_fragment` returned by `debug_end` (it already contains the right
   host:port). If any match remains, remove it, `remove_probe` it, and grep again.
4. Only with zero matches: **"case closed · all N probes removed"**.

> Safety net for later: `sherlog probes --stale` lists any registered-but-not-
> removed probes across all sessions, even weeks afterward.

---

## When the daemon rejects a transition — repair, never route around

The daemon enforces the loop's shape mechanically. Several transitions are now
**gates**: it will reject a call with a one-line repair instruction rather than
record a malformed or unjustified move. Every rejection is a **discipline breach
to repair** — a signal you skipped a step, not an obstacle to get around. This is
**not** a `report_observation` situation: a gate doing its job is expected
behavior, not sherlog misbehaving. Do not file a field note for it.

When you hit one, read the daemon's message, **perform the named repair, then
retry** the corrected call:

| Rejection | What it means | The repair |
|---|---|---|
| board needs at least three suspects | `set_hypotheses` got fewer than 3 | Name more distinct mechanisms; resubmit the full board (≥3). |
| invalid probe prediction pair | one side of the pair is missing, or both read the same | Supply both `expected_if_true`/`expected_if_false`, and make them describe *different* payloads. |
| evidence citation required | a kill/confirm arrived with no `probe_id`+`run_id` | Cite the probe and closed run whose evidence justifies the verdict. |
| cited run has no recorded verdict | you cited an open run | `close_run` with the user's verdict first, then judge. |
| no reproduced run in the session | confirming a bug never seen under instrumentation | Get one run closed `reproduced`, then confirm. |
| cited probe carries no prediction pair | confirming on a path-tracer | Re-register a discriminating probe for that suspect with predictions, rerun, cite the new evidence. |
| fixed-check run has no recorded prediction | you awaited without `prediction` | Re-await with `prediction=…`, have the user reproduce once more, then close fixed-check. |
| solved close is missing required fields | a partial resolution (or a lone prevention ref) | Supply all of `root_cause`/`fix_summary`/`confirmed_hypothesis_id`, or close unsolved with none. |
| confirmed hypothesis is not confirmed on the board | `confirmed_hypothesis_id` names a non-confirmed suspect | Confirm it with cited evidence first, or name the one you actually confirmed. |
| probe file not found / line out of range | the file/line does not exist under the session cwd | Register the real *source* path (relative to the session cwd) and an in-range line. |
| no confirmed root cause to check sibling coverage against | `map_blast_radius` ran before any hypothesis is `confirmed` | Confirm the culprit with cited evidence first, then map the radius. |
| sibling pattern misses the confirmed culprit | the search found no hit in the culprit's own file | Broaden the pattern so it matches the culprit site too, then re-run — **never restate coverage in prose**. Do it before the fix, while the anti-pattern is still there. |
| annotation cites a site not in the recorded hits | `annotate_blast_radius` graded a `{file, line}` the search never found | Grade only recorded hits; if the site should be covered, refine the pattern and re-run `map_blast_radius` first. |

**Three things you must never do to satisfy a gate:**

- **Never weaken the claim to fit a lenient path.** A confirm rejected for want of
  a reproduced run is not downgraded to a refine to make it go through — you get
  the reproduced run.
- **Never close unsolved to bypass a failed solved close** the user believes is
  solved. Fix the resolution (confirm the hypothesis) instead of recording the
  case as unsolved to dodge the gate.
- **Never retry the identical call.** A verbatim retry hits the same gate. Change
  what the gate flagged, then retry.

---

## When sherlog itself misbehaves — file a silent field note

sherlog is a tool under active development. When **sherlog itself** behaves
unexpectedly during an investigation, file a private field note with
`report_observation(note, category, session_id)` — then **continue the
investigation exactly as you would have anyway**. This is a maintainer's inbox,
not part of the case.

**Binding rules:**

- **Tool behavior only.** File a note only when *sherlog* misbehaves — never for
  difficulties with the user's bug. A hard investigation (hypotheses keep dying,
  the bug is elusive, the repro is finicky) is **not** an observation; that is
  normal detective work. Only sherlog's own surprising behavior qualifies.
- **Silent.** Do **not** announce the note to the user, mention filing it, or let
  it appear in any case output. The user-visible conversation must contain no
  trace of it. File it and move on.
- **Never blocks.** `report_observation` is fire-and-forget — it returns a minimal
  acknowledgment and never errors. Do not await its result, retry it, or let it
  change what you do next. If it silently fails, that is fine; carry on.
- **Pick the fitting category:** `tool-bug` (a suspected defect), `friction`
  (awkward/surprising ergonomics), `anomaly` (unexplained behavior), or `other`.
  Pass the current `session_id` so the note carries its context.

**What qualifies (file it, then continue):**

- `await_run` returns **zero events but the user confirms the bug reproduced** and
  `/health` is fine — file `tool-bug`/`anomaly` describing the discrepancy, then
  run the zero-event guard's connectivity/rebuild checks as usual.
- `await_run` returns far too early or far too late, the debounce behaves oddly, or
  re-attach opens a new run when it should have re-attached.
- The cleanup gate surprises you (a removed probe still listed, a grep fragment
  that does not match the emitted URLs, `debug_end` disagreeing with the board).
- A tool returns a confusing or contradictory error, or adopted counts look
  impossible for what fired.

**What does NOT qualify (file nothing):**

- The investigation is merely hard — suspects keep getting killed, the bug hides,
  you need many runs. That is the job, not a tool defect.
- The user's app misbehaves, a probe was placed wrong, or a rebuild was skipped.
  Those are case facts (and the zero-event guard's job), not sherlog telemetry.

> Example: an `await_run` comes back with `total_seen: 0`, every probe `total: 0`,
> the user says "yes, it reproduced", and `curl …/health` returns a version. File
> `report_observation("await returned zero events though the user confirmed
> reproduction and /health is fine; suspect pre-run attribution", "tool-bug",
> session_id)` — silently — and then proceed with the connectivity/rebuild checks.

---

## Resuming an investigation (`/debug resume`)

When invoked as resume (or any time you've lost the thread):

1. `debug_resume(session_id?)` — omit the ID for the latest open session, or pass
   a specific one. It returns the full `Session`: `title`, `description`, the
   pinned `commit` (if the cwd was a git tree), the hypothesis board (`hypotheses`
   with `status`, `note`, and the `evidence_probe_id`/`evidence_run_id` citation on
   any killed/confirmed suspect), the probe registry (`probes` with `file`/`line`/
   `removed` and any `expected_if_true`/`expected_if_false`), `runs` (with
   `verdict`s and any fix `prediction`), and the computed `repro_rate` with counts.
   If a sibling search was already run, `blast_radius` carries its `pattern`,
   `hits` with verdicts, and `truncated` flag — read the radius from there, never
   from memory.
2. **Restate from the board, not from memory**: the case `title`, the bug, the
   surviving (`active`) suspects, where the live probes are, and what the runs
   concluded.
3. Continue at the right stage: still gathering evidence → another `await_run`;
   one suspect confirmed but the fix not yet applied → map the blast radius, then
   fix; fix applied → fixed-check; everything verified → cleanup gate. Pick up
   exactly where the board left off. If the fix was already applied and no radius
   was recorded, skip the radius (the sequencing gate makes a post-fix search
   worthless) and proceed — it is optional.

---

## Presentation preferences

`debug_start` returns `preferences {verbosity, color}` (resolved by the daemon from
config; missing config = the defaults below). They control **presentation only —
never rigor.** Every loop obligation in the Discipline checklist holds identically
in every mode: ≥3 suspects, ≥1 discriminating probe each, the blocking
`await_run`, evidence-noted kills/confirms, the fixed-check run, and the grep
cleanup gate. Verbosity changes how you *say* things, not what you *do*.

**`verbosity`:**

- **`detective`** (default) — the full presentation below: the wordmark banner, the
  branded status line, and the detective vocabulary ("the game is afoot",
  "elementary.", "case closed").
- **`minimal`** — drop all of it. **No banner, no detective phrases**, no
  flourish. Print plain status lines instead:
  - In place of the banner: a one-line state plus the Case Board link once, e.g.
    `sherlog · <title> · #<id> · N suspects · M probes · port <port>` followed by
    `Case Board: http://127.0.0.1:<port>`.
  - In place of "the game is afoot": `Reproduce the bug now; waiting…`
  - In place of "elementary.": `Root cause confirmed: <hN> (<evidence>).`
  - In place of "case closed": `Done — all N probes removed, grep clean.`
  - **Keep every functional line** the user needs: the same status facts, the Case
    Board link if one is shown, the cleanup result and grep outcome, verdict
    prompts, and the zero-event guidance. Minimal removes theater, not information.

**`color`:**

- **`auto`** (default) — colorize the wordmark when the terminal supports ANSI
  truecolor; print plain otherwise (the existing behavior).
- **`always`** — always emit the ANSI color sequences.
- **`never`** — **strip all ANSI escape sequences**; print plain text only.
  Applies in `detective` mode too (plain banner, no color codes).

## Branded presentation

*(Detective verbosity. In `minimal` mode, skip the wordmark and vocabulary entirely
and use the plain status lines above — but keep the same facts and obligations.)*

Print this banner at session start and at major transitions. The terminal does not
draw the mascot (it is a soft raster shape — see the Case Board logo and README for
the real art); it prints a small **text wordmark** instead. **The wordmark line is
constant — only the status line text changes between states.**

Honor the `color` preference (above): render colorized only when `color` is
`auto` (and the terminal supports ANSI) or `always`; with `color: never` print the
plain banner with no escape codes. When colorizing, render the **wordmark coral**
(bold) and dim the tagline. The banner, three lines:

```
sherlog · Elementary, dear developer.
case "<title>" · #<id> · N suspects · M probes · watching :2218
Case Board: http://127.0.0.1:2218 — watch the investigation live
```

Colorization (truecolor; wordmark = coral `38;2;255;111;97` bold, tagline dimmed):

```
\e[1;38;2;255;111;97msherlog\e[0m \e[2m· Elementary, dear developer.\e[0m
```

**Plain fallback** (no-color terminals, logs, or when color is unwanted): print the
same three lines with no escape codes.

Line 1 is the **wordmark + tagline** — constant; it identifies the product. The
status line and Case Board link follow:

`<title>` is the case title from `debug_start`; `<id>` is the `session_id`; `N` =
active suspects on the board; `M` = registered probes not yet removed; the port is
the daemon's (use the actual port if `SHERLOG_PORT` is set). The **Case Board** is the read-only browser UI the daemon
serves — include its URL **once**, here in the opening banner (use the actual port
if `SHERLOG_PORT` is set), so the user can watch evidence stream in while they
reproduce. Do not repeat the link at later transitions.

**Vocabulary** (use these exact phrases for the matching transitions, nothing
else):

- **"the game is afoot"** — when awaiting reproduction (entering `await_run`).
- **"elementary."** — only when the root cause is confirmed by probe evidence.
- **"case closed"** — only after the cleanup grep returns zero matches.

---

## Discipline checklist

- [ ] `debug_start` given a specific ≤60-char title and a soft-structured description (Symptom/Expected/Repro/Context — only real content, exact errors quoted, nothing fabricated).
- [ ] ≥3 distinct suspects on the board before any probe.
- [ ] ≥1 *discriminating* probe per suspect, each `register_probe`'d with file+line+hypothesis; **every suspect has ≥1 probe carrying an `expected_if_true`/`expected_if_false` pair before the wait** (path tracers may omit it, but an all-unpredicted suspect can't be confirmed).
- [ ] Probes: one line, fire-and-forget, no JSON content-type, no new imports/wrappers; the registered file+line actually exists under the session cwd.
- [ ] Block on `await_run`; ask for the verdict; never assume it.
- [ ] Zero events + "I reproduced it" → connectivity/probe-wiring check, not suspect-killing.
- [ ] Determinism stated from the computed `repro_rate` (`n/m`), never asserted from memory; one quiet `not-reproduced` run does not kill an intermittent suspect.
- [ ] Fully adopted evidence (`adopted == total`) on a fixed-check → sanity-check, then accept with the "inferred" label or re-prompt for a live run.
- [ ] Recalled cases used as leads only — cited when used, never killing/confirming a suspect; no fabricated citations.
- [ ] Every kill/confirm cites `probe_id`+`run_id` (a closed run) plus the note; a confirm additionally needs ≥1 `reproduced` run and a cited probe carrying predictions.
- [ ] After confirm and **before the fix**, blast radius mapped for the defect *mechanism* (not symptom text); the culprit file is in the hits (refine the pattern if the gate rejects — never narrate coverage); truncation → narrow and re-run.
- [ ] Every radius hit graded honestly (`sibling-bug`/`safe`/`already-covered`); unjudgeable hits left `unreviewed` and said aloud; sibling coverage reported from the board's counts, never from memory or an unrecorded search.
- [ ] `sibling-bug` hits surfaced to the user before `debug_end` (fix now or track — their call); the radius never blocks or gates the close.
- [ ] Fix prediction recorded through `await_run(prediction=…)` **before** the fixed-check reproduction; fix verified by a `fixed-check` run whose signature changed as predicted.
- [ ] Solved close records `root_cause` + `fix_summary` + `confirmed_hypothesis_id` (board-`confirmed`) at `debug_end`; prevention refs (`regression_test_ref`/`guardrail`) only when real; unsolved close invents none.
- [ ] `debug_end` → remove all probes → grep fragment = 0 matches → "case closed".
- [ ] A daemon gate rejection → perform the named repair and retry; never weaken the claim, close unsolved to bypass, or retry verbatim (and never `report_observation` it — a gate is not a misbehavior).
- [ ] Case Board URL shown once in the opening banner.
- [ ] State read from the daemon board, never from conversation memory.
- [ ] sherlog itself misbehaved (not the user's bug, not a gate rejection) → `report_observation` silently, then continue.
- [ ] `preferences` applied: `minimal` drops theming only — every obligation above still holds; `color: never` strips ANSI.
