package store

import "fmt"

// ReproRate is the computed determinism signal for a session
// (harden-detective-gates D-I): how often the bug reproduced across closed
// repro-attempt runs, carried with its raw counts so the agent and user see the
// fraction (3/5) rather than an asserted rate. It is never stored — derived at
// read time from run verdicts — and its only gating use in this change is D-C's
// "≥1 reproduced run".
type ReproRate struct {
	Reproduced    int `json:"reproduced"`
	NotReproduced int `json:"not_reproduced"`
	// Rate is Reproduced / (Reproduced + NotReproduced), or 0 when no repro-attempt
	// run has closed (an empty denominator is reported as a zero rate with zero
	// counts, never a divide-by-zero).
	Rate float64 `json:"rate"`
}

// ComputeReproRate derives the repro rate from a run slice (D-I): reproduced over
// (reproduced + not-reproduced) across CLOSED runs, excluding fixed-check runs
// from the denominator and ignoring runs still open (no verdict yet). It is a pure
// function so the daemon and Case Board can compute it from a session snapshot
// without touching the store, and so it stays trivially testable.
func ComputeReproRate(runs []Run) ReproRate {
	var rr ReproRate
	for _, r := range runs {
		if r.ClosedAt == nil {
			continue // still open: no verdict to count
		}
		switch r.Verdict {
		case VerdictReproduced:
			rr.Reproduced++
		case VerdictNotReproduced:
			rr.NotReproduced++
			// VerdictFixedCheck (and any unrecognized verdict) is excluded from the
			// denominator: a fix-verification run is not a determinism sample (D-I).
		}
	}
	if denom := rr.Reproduced + rr.NotReproduced; denom > 0 {
		rr.Rate = float64(rr.Reproduced) / float64(denom)
	}
	return rr
}

// ReproRate computes the session's repro rate under the store lock (D-I). It never
// persists the result — the value is derived from the live run verdicts each call.
func (s *Store) ReproRate(sessionID string) (ReproRate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return ReproRate{}, fmt.Errorf("repro rate for %q: %w", sessionID, ErrSessionNotFound)
	}
	return ComputeReproRate(entry.session.Runs), nil
}
