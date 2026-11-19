# Design: harden-detective-gates

## Context

The store today validates almost nothing about the loop's *shape*: `SetHypotheses` accepts any board size, `UpdateHypothesis` accepts `confirmed` with an optional, unchecked note, `CloseSessionWithResolution` never compares `confirmed_hypothesis_id` against the board, and `register_probe` records whatever file/line it is told. Every rigor rule lives in `skills/debug/SKILL.md`. Meanwhile the evidence layer is already hardened (daemon-ingested events, zero-fill, adoption and truncation disclosure) — the asymmetry is the problem this change closes.

**Threat model (binding for scope decisions):** the gates target *drift, sloppiness, and hallucination* in an agent honestly running the loop — omission, malformation, fabricated citations, unrecorded predictions. They do **not** target an adversarial agent: the agent writes the probe code and could plant lying probes; no daemon check can beat that. Any proposed check whose only value is defeating deliberate deception is out of scope.

## Goals / Non-Goals

**Goals:**

- Every discipline rule that can be checked mechanically is checked by the daemon/store, not the skill prose.
- Schema checks catch omission/malformation; cross-checks against the store's own data catch fabricated citations; recorded-before-returned predictions catch post-hoc rationalization.
- Rejections are actionable errors that tell the agent how to repair the board, never silent downgrades.
- All persistence stays additive: pre-change `state.json` loads and resumes.

**Non-Goals:**

- Ticket trackers, sandboxes, git-bisect automation, CI/test-suite/coverage gates, PR authoring (agent-layer concerns; sherlog records references at most).
- Semantic validation of prediction text against probe payloads (judgment stays with the agent; the daemon guarantees the prediction *exists and predates the evidence*).
- Defeating a deliberately deceptive agent (see threat model).

## Decisions

### D-A · Predictions live on the Probe, as an optional-but-validated pair

`Probe` gains `expected_if_true` and `expected_if_false` (strings). Validation at `register_probe`: **both or neither**, and when present they must differ (case-insensitive, whitespace-trimmed compare). Predictions are *not* required on every probe — path tracers stay legal — but the **confirm gate (D-C) only accepts a citation to a probe that carries them**, which is where the mechanical pressure belongs.

*Alternative considered:* predictions on the Hypothesis. Rejected — the prediction describes what a specific probe's payload looks like under true/false; one hypothesis may have several probes with different expected signatures.

### D-B · Evidence citations are structured params, cross-checked in the store

`update_hypothesis` with status `killed` or `confirmed` requires `probe_id` + `run_id`. The store validates: the probe is registered in the session, the run exists **and is closed** (verdict recorded — enforcing the ask-verdict-then-judge sequence). A `total: 0` bucket is a valid citation — "fired zero times" is load-bearing evidence (zero-fill). The citation is persisted on the hypothesis (`evidence_probe_id`, `evidence_run_id`) alongside the existing free-text note; status `active` (refine) requires no citation.

*Alternative considered:* also requiring the cited probe to have `total > 0`. Rejected — it would outlaw the zero-evidence kill, sherlog's signature move.

### D-C · Confirm gate

`confirmed` additionally requires: (1) the session has ≥1 closed run with verdict `reproduced`; (2) the cited probe carries predictions (D-A). Rationale: you cannot confirm a root cause for a bug never observed under instrumentation, and the confirming experiment must have been a pre-registered experiment, not a post-hoc story.

*Known loophole:* the gate reads the cited probe's *current* predictions and `register_probe` upserts by ID, so a rejected confirm can be satisfied by re-registering the same probe with a post-hoc pair and citing the already-closed run. Accepted per the threat model (drift, not deception) — the skill's repair path mandates a rerun; the binary does not force it. Documented beside the D-D loophole in `docs/architecture.md`.

### D-D · Fix predictions are stamped on the Run before evidence returns

`await_run` gains optional `prediction` (how the evidence should change if the fix is right). The daemon stores it **at call receipt** — before any summary is returned from that call — with a `prediction_at` timestamp, only if the run does not already carry one (immutable once set; supplying it on a re-attach whose run has none is accepted). `close_run(verdict=fixed-check)` is rejected when the open run has no prediction; the error instructs the skill to re-await with a prediction and have the user reproduce once more. `diff_runs` output includes the fixed-check run's prediction so the contrast is judged against the recorded claim, not conversation memory.

*Known loophole:* an agent that awaited without a prediction, read partial evidence, then re-attached and supplied one has technically seen data first. Accepted per the threat model — the realistic failure (never predicting at all) is caught, and closing the loophole would require a run-discard concept that isn't worth its weight.

### D-E · Board minimum enforced at the store

`SetHypotheses` rejects fewer than 3 statements (`set_hypotheses` keeps replace semantics — a mid-investigation split still submits the full board of ≥3). The skill's "≥3 distinct suspects" stops being advisory.

### D-F · Solved close validated against the board

A `debug_end` supplying any resolution field must supply all of `root_cause`, `fix_summary`, `confirmed_hypothesis_id` (the documented contract, now enforced), and `confirmed_hypothesis_id` must reference a hypothesis whose status is `confirmed`. Mismatch → error, **not** a silent downgrade to unsolved (fail fast; never suppress). An unsolved close (no resolution fields) is untouched and remains always available.

### D-G · Probe location check runs in the daemon, against the session cwd

`register_probe` resolves `file` against the session's stored `cwd` (absolute paths used as-is), requires the file to exist and `line` to be within `[1, line-count]`. Daemon-side because the daemon owns investigation state (D6) and the MCP process's cwd is not guaranteed to be the session's; both run on the same machine, so the daemon can stat the path. Miss → actionable error naming the resolved path.

*Alternative considered:* MCP-side check (matches the existing client-side enum-validation pattern). Rejected — single enforcement point in the daemon covers any future caller, and only the daemon reliably knows the session cwd.

### D-H · Commit SHA captured by the daemon at debug_start

The daemon runs `git -C <cwd> rev-parse HEAD` (stdlib `os/exec`, short timeout) at session creation; on any failure (no git, not a repo) the field is silently omitted. Stored as `Session.Commit`, shown on the board and in `debug_resume`. Recording only — no gate consumes it in this change.

### D-I · Repro rate is computed, never stored

`reproduced / (reproduced + not-reproduced)` over the session's closed runs (fixed-check runs excluded from the denominator). Exposed in `await_run` results, `debug_resume`, and the Case Board case header. It gives the agent and user a computed — not asserted — determinism signal; D-C's "≥1 reproduced run" is its only gating use in this change.

### D-J · Resolution references are recorded, never executed

`Resolution` gains `regression_test_ref` (string) and `guardrail` (`{type, ref}`, `type ∈ {test, lint, alert, doc}` — enum validated; `ref` free text). Displayed on the closed-case view. Sherlog never resolves, fetches, or runs them — local-only invariant.

### D-K · Error surface and skill posture

Store gates return typed errors → daemon 4xx with a one-line repair instruction → MCP surfaces them verbatim as tool errors. The skill gains one rule: a gate rejection is a discipline breach to repair (fix the board, register the missing prediction, re-run), never an error to route around or retry verbatim.

## Risks / Trade-offs

- [Stricter gates can stall a mid-flight legacy session — e.g. confirming on a board whose probes predate predictions] → Gates apply to *new transitions only*; the repair path is cheap (re-register the confirming probe with predictions, one more run) and the error says exactly that.
- [File check false-positives in generated/moved trees (bundler output, repo relocated mid-case)] → Error names the resolved path; skill instructs registering the source-file path relative to the session cwd. No override flag — an unfindable probe is exactly what the cleanup gate cannot afford.
- [Prediction loophole on re-attach (D-D)] → Documented; out of threat model.
- [`os/exec` of git from the daemon adds an execution surface] → Fixed argv (no shell), short timeout, output discarded on error, local-only daemon; acceptable.
- [Repro rate is misleading for env-specific bugs (denominator mixes environments)] → Presented as a signal with its raw counts (`3/5`), never as a gate beyond ≥1 reproduced.

## Migration Plan

1. Store/types + gates land behind the same version (all `state.json` fields additive; legacy files load with fields absent).
2. MCP schemas, daemon API, Case Board, and `skills/debug/SKILL.md` update **in the same release** — the shipped skill is the only tool caller, so binary and skill move together (plugin release ships both).
3. Docs (`tools-reference.md`, `architecture.md`) in the same PR per the docs-match-binary convention.
4. Rollback = previous release; new fields in `state.json` are ignored by old binaries (unknown-field-tolerant JSON decoding), so downgrade is safe.
5. Dev note: after `go install`, kill the resident daemon so the new binary respawns.

## Open Questions

(none — all decisions above are settled for this change)
