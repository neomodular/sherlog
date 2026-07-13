package store

import (
	"errors"
	"fmt"
	"strings"
)

// The detective-loop gates (harden-detective-gates) move discipline rules that can
// be checked mechanically out of the skill prose and into the store. Each gate
// returns a typed sentinel wrapped with a one-line, actionable repair instruction
// (D-K): the daemon maps the sentinel to a 4xx and surfaces the message verbatim so
// the agent repairs the board rather than routing around the error.
var (
	// ErrInsufficientHypotheses is returned when SetHypotheses carries fewer than
	// the minimum board size (D-E).
	ErrInsufficientHypotheses = errors.New("board needs at least three suspects")

	// ErrPredictionPair is returned when a probe's expected_if_true/expected_if_false
	// pair is malformed: only one side supplied, or the two are equal (D-A).
	ErrPredictionPair = errors.New("invalid probe prediction pair")

	// ErrEvidenceCitationRequired is returned when a kill/confirm transition omits
	// the probe_id/run_id citation (D-B).
	ErrEvidenceCitationRequired = errors.New("evidence citation required for a kill or confirm")

	// ErrCitedRunNotClosed is returned when a citation names a run that has not been
	// closed with a verdict (D-B: ask-verdict-then-judge).
	ErrCitedRunNotClosed = errors.New("cited run has no recorded verdict")

	// ErrNoReproducedRun is returned when a confirm is attempted before any run in
	// the session has closed with verdict reproduced (D-C).
	ErrNoReproducedRun = errors.New("no reproduced run in the session")

	// ErrCitedProbeUnpredicted is returned when a confirm cites a probe that carries
	// no prediction pair (D-C).
	ErrCitedProbeUnpredicted = errors.New("cited probe carries no prediction pair")

	// ErrFixedCheckNeedsPrediction is returned when a run is closed with verdict
	// fixed-check but carries no recorded fix prediction (D-D).
	ErrFixedCheckNeedsPrediction = errors.New("fixed-check run has no recorded prediction")

	// ErrResolutionIncomplete is returned when a solved close supplies some but not
	// all of root_cause, fix_summary, confirmed_hypothesis_id (D-F).
	ErrResolutionIncomplete = errors.New("solved close is missing required resolution fields")

	// ErrUnconfirmedHypothesis is returned when a solved close names a
	// confirmed_hypothesis_id that is not confirmed on the board (D-F).
	ErrUnconfirmedHypothesis = errors.New("confirmed hypothesis is not confirmed on the board")

	// ErrInvalidGuardrailType is returned when a resolution guardrail carries a type
	// outside the allowed set (D-J).
	ErrInvalidGuardrailType = errors.New("invalid guardrail type")

	// ErrInvalidHypothesisStatus is returned when a hypothesis update names a status
	// outside the lifecycle enum. Without this gate any unknown status ("", "garbage")
	// would skip the kill/confirm citation checks entirely and still be written to the
	// board — a gate bypass found live by the release dogfood.
	ErrInvalidHypothesisStatus = errors.New("invalid hypothesis status")

	// ErrProbeIDRequired is returned when a probe registration carries a blank id.
	// The MCP tool always supplies one; this guards raw /api/ callers whose empty
	// id would otherwise persist and zero-fill as ("", 0) in every await summary —
	// found live by the restart-on-upgrade dogfood.
	ErrProbeIDRequired = errors.New("probe id required")

	// ErrInvalidResolutionText is returned when a resolution text field carries
	// control characters. Resolution fields are single-line plain text — they feed
	// recall and the Case Board — and a past close persisted raw multi-line
	// tool-call fragments into a root cause, found live by the same dogfood.
	ErrInvalidResolutionText = errors.New("invalid resolution text")
)

// minHypotheses is the board floor enforced by SetHypotheses (D-E): a real
// differential diagnosis names at least three distinct suspects.
const minHypotheses = 3

// validateHypothesisStatus rejects any status outside the lifecycle enum before a
// hypothesis update touches the board, so an unknown status can never bypass the
// citation gates that key off killed/confirmed.
func validateHypothesisStatus(status HypothesisStatus) error {
	switch status {
	case HypothesisActive, HypothesisKilled, HypothesisConfirmed:
		return nil
	}
	return fmt.Errorf("hypothesis status %q is not one of active, killed, confirmed: %w", status, ErrInvalidHypothesisStatus)
}

// validatePredictionPair enforces the discriminating-prediction contract (D-A):
// both sides or neither, and — when present — the two must differ after trimming
// whitespace and ignoring case, so a prediction that "proves nothing because both
// outcomes look the same" is rejected. Values are validated but not mutated; the
// probe persists them exactly as supplied.
func validatePredictionPair(expectedIfTrue, expectedIfFalse string) error {
	t := strings.TrimSpace(expectedIfTrue)
	f := strings.TrimSpace(expectedIfFalse)
	hasT, hasF := t != "", f != ""

	if hasT != hasF {
		missing := "expected_if_false"
		if !hasT {
			missing = "expected_if_true"
		}
		return fmt.Errorf("%w: %s is missing — supply both sides of the prediction or neither", ErrPredictionPair, missing)
	}
	if hasT && strings.EqualFold(t, f) {
		return fmt.Errorf("%w: expected_if_true and expected_if_false are identical, so the probe proves nothing — make the two outcomes look different", ErrPredictionPair)
	}
	return nil
}

// validateCitationLocked cross-checks an evidence citation against the session's
// own registry before a kill/confirm is accepted (D-B, D-C). Callers hold s.mu.
// A zero-fired probe is a valid citation — "fired zero times" is evidence — so no
// event count is inspected here.
func validateCitationLocked(sess *Session, status HypothesisStatus, probeID, runID string) error {
	if probeID == "" || runID == "" {
		return fmt.Errorf("%w: cite the probe_id and run_id whose evidence justifies this verdict", ErrEvidenceCitationRequired)
	}

	probe := findProbe(sess, probeID)
	if probe == nil {
		return fmt.Errorf("cited probe %q is not registered in this session: %w", probeID, ErrProbeNotFound)
	}
	run := findRun(sess, runID)
	if run == nil {
		return fmt.Errorf("cited run %q does not exist in this session: %w", runID, ErrRunNotFound)
	}
	if run.ClosedAt == nil || run.Verdict == "" {
		return fmt.Errorf("cited run %q must be closed with a verdict first — record the run's verdict, then judge: %w", runID, ErrCitedRunNotClosed)
	}

	// Confirm gate (D-C): a root cause can only be confirmed for a bug observed under
	// instrumentation, via a pre-registered (predicted) experiment.
	if status == HypothesisConfirmed {
		if !hasReproducedRun(sess) {
			return fmt.Errorf("cannot confirm a root cause for a bug never reproduced under instrumentation — get at least one run closed reproduced first: %w", ErrNoReproducedRun)
		}
		if probe.ExpectedIfTrue == "" || probe.ExpectedIfFalse == "" {
			return fmt.Errorf("the confirming probe %q must carry expected_if_true / expected_if_false — re-register it with a prediction pair: %w", probeID, ErrCitedProbeUnpredicted)
		}
	}
	return nil
}

// validateResolutionLocked enforces the solved-close contract (D-F, D-J) before any
// mutation, so a rejection leaves the session OPEN rather than silently downgrading
// it to unsolved. Callers hold s.mu and have already confirmed the resolution is
// non-empty (a nil/empty resolution is an unsolved close and skips this entirely).
func validateResolutionLocked(sess *Session, res *Resolution) error {
	if res.RootCause == "" || res.FixSummary == "" || res.ConfirmedHypothesisID == "" {
		return fmt.Errorf("a solved close needs root_cause, fix_summary, and confirmed_hypothesis_id (or close unsolved with none): %w", ErrResolutionIncomplete)
	}
	// Malformation before cross-checks: resolution fields are single-line plain
	// text (they feed recall and the board), so control characters — the shape of
	// pasted multi-line tool-call fragments — are rejected at the door.
	for _, f := range []struct{ name, value string }{
		{"root_cause", res.RootCause},
		{"fix_summary", res.FixSummary},
		{"regression_test_ref", res.RegressionTestRef},
	} {
		if err := validateResolutionText(f.name, f.value); err != nil {
			return err
		}
	}
	if res.Guardrail != nil {
		if err := validateResolutionText("guardrail ref", res.Guardrail.Ref); err != nil {
			return err
		}
	}
	h := findHypothesis(sess, res.ConfirmedHypothesisID)
	if h == nil || h.Status != HypothesisConfirmed {
		return fmt.Errorf("confirmed_hypothesis_id %q is not confirmed on the board — confirm it with evidence before closing solved: %w", res.ConfirmedHypothesisID, ErrUnconfirmedHypothesis)
	}
	if res.Guardrail != nil && !validGuardrailType(res.Guardrail.Type) {
		return fmt.Errorf("guardrail type %q is not one of test, lint, alert, doc: %w", res.Guardrail.Type, ErrInvalidGuardrailType)
	}
	return nil
}

// validateResolutionText rejects control characters in a resolution text field,
// keeping the field single-line plain text fit for recall and display.
func validateResolutionText(field, value string) error {
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return fmt.Errorf("%s must be single-line plain text — remove newlines and control characters: %w", field, ErrInvalidResolutionText)
		}
	}
	return nil
}

// hasReproducedRun reports whether the session has at least one run closed with
// verdict reproduced (D-C).
func hasReproducedRun(sess *Session) bool {
	for i := range sess.Runs {
		if sess.Runs[i].ClosedAt != nil && sess.Runs[i].Verdict == VerdictReproduced {
			return true
		}
	}
	return false
}

// findProbe / findRun / findHypothesis are pointer lookups into a session's slices
// (callers hold s.mu). They return nil when the ID is unknown.
func findProbe(sess *Session, id string) *Probe {
	for i := range sess.Probes {
		if sess.Probes[i].ID == id {
			return &sess.Probes[i]
		}
	}
	return nil
}

func findRun(sess *Session, id string) *Run {
	for i := range sess.Runs {
		if sess.Runs[i].ID == id {
			return &sess.Runs[i]
		}
	}
	return nil
}

func findHypothesis(sess *Session, id string) *Hypothesis {
	for i := range sess.Hypotheses {
		if sess.Hypotheses[i].ID == id {
			return &sess.Hypotheses[i]
		}
	}
	return nil
}
