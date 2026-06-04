package store

import (
	"fmt"
	"sort"
)

// divergenceRatio is the count ratio beyond which two runs' probe counts are
// flagged divergent even when both fired (design D6: ≥10×). A probe firing 10×
// more in one run than the other is a signal worth pinning to the top.
const divergenceRatio = 10

// ProbeDiff is the per-probe comparison between two runs (design D6). Each side
// carries fired/count/first-last sample disclosure mirroring a query result, so a
// reader sees both ends of each run's evidence and any flood truncation. Divergent
// flags the probe as a differential-diagnosis candidate: fired in exactly one run,
// or a count ratio ≥10×.
type ProbeDiff struct {
	Probe     string   `json:"probe"`
	A         DiffSide `json:"a"`
	B         DiffSide `json:"b"`
	Divergent bool     `json:"divergent"`
}

// DiffSide is one run's view of a probe in a diff: whether it fired, its true
// total, the truncation flag, and the first/last retained sample events (design
// D6). Empty Events with Fired false means the probe never fired in this run.
type DiffSide struct {
	Run       string    `json:"run"`
	Fired     bool      `json:"fired"`
	Total     int       `json:"total"`
	Adopted   int       `json:"adopted"`
	Truncated bool      `json:"truncated"`
	First     *LogEvent `json:"first,omitempty"`
	Last      *LogEvent `json:"last,omitempty"`
}

// RunDiff is the full comparison of two runs of one session (design D6): the run
// IDs and the per-probe diffs with divergent probes pinned to the top.
type RunDiff struct {
	Session string      `json:"session"`
	RunA    string      `json:"run_a"`
	RunB    string      `json:"run_b"`
	Probes  []ProbeDiff `json:"probes"`
}

// ErrSameRun is returned when a diff names the same run for both sides — there is
// nothing to compare.
var ErrSameRun = errSentinel("diff requires two distinct runs")

// errSentinel is a tiny helper so package error vars stay declarative without a
// per-error type. It mirrors errors.New but reads as a constant declaration.
type errSentinel string

func (e errSentinel) Error() string { return string(e) }

// DiffRuns compares two runs of the same session per probe (design D6). Both runs
// must exist in the named session; naming an unknown run or the same run twice is
// rejected with a clear error (log-query spec: invalid run pair). Probes that
// fired in either run are reported; divergent probes — fired in exactly one run,
// or count ratio ≥10× — are flagged and sorted first. The computation reads only
// the already-retained flood-controlled buffers, so it adds no storage and carries
// the same truncation disclosures as a query (design D6).
func (s *Store) DiffRuns(sessionID, runA, runB string) (RunDiff, error) {
	if runA == runB {
		return RunDiff{}, fmt.Errorf("diff runs %q in %q: %w", runA, sessionID, ErrSameRun)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return RunDiff{}, fmt.Errorf("diff runs in %q: %w", sessionID, ErrSessionNotFound)
	}
	// Both runs must belong to THIS session: a diff across sessions is meaningless,
	// and an unknown run has nothing to compare (log-query spec: invalid run pair).
	if !runExists(entry.session, runA) {
		return RunDiff{}, fmt.Errorf("diff runs in %q: run %q: %w", sessionID, runA, ErrRunNotFound)
	}
	if !runExists(entry.session, runB) {
		return RunDiff{}, fmt.Errorf("diff runs in %q: run %q: %w", sessionID, runB, ErrRunNotFound)
	}

	// Collect every probe that fired in either run, keyed by probe ID.
	sideA := sidesByProbe(entry, runA)
	sideB := sidesByProbe(entry, runB)
	probes := map[string]struct{}{}
	for p := range sideA {
		probes[p] = struct{}{}
	}
	for p := range sideB {
		probes[p] = struct{}{}
	}

	diffs := make([]ProbeDiff, 0, len(probes))
	for probe := range probes {
		a := sideA[probe]
		a.Run = runA
		b := sideB[probe]
		b.Run = runB
		diffs = append(diffs, ProbeDiff{
			Probe:     probe,
			A:         a,
			B:         b,
			Divergent: divergent(a, b),
		})
	}

	// Divergent probes first (the differential-diagnosis signal), then by probe ID
	// for stable output within each group (design D6: divergent pinned to top).
	sort.Slice(diffs, func(i, j int) bool {
		if diffs[i].Divergent != diffs[j].Divergent {
			return diffs[i].Divergent
		}
		return diffs[i].Probe < diffs[j].Probe
	})

	return RunDiff{Session: sessionID, RunA: runA, RunB: runB, Probes: diffs}, nil
}

// sidesByProbe builds a DiffSide per probe that fired in run, from the run's flood
// buffers (callers hold s.mu). First/Last are the chronological extremes of the
// retained events; for a non-truncated buffer they are the true first and last
// hits, and for a truncated one they are the retained first-N head and last-N tail
// (the truncation flag discloses the dropped middle).
func sidesByProbe(entry *sessionEntry, run string) map[string]DiffSide {
	out := map[string]DiffSide{}
	for key, buf := range entry.floods {
		if key.run != run {
			continue
		}
		events := buf.events()
		side := DiffSide{
			Fired:     buf.total > 0,
			Total:     buf.total,
			Adopted:   buf.adopted,
			Truncated: buf.truncated(),
		}
		if len(events) > 0 {
			first := events[0]
			last := events[len(events)-1]
			side.First = &first
			side.Last = &last
		}
		out[key.probe] = side
	}
	return out
}

// divergent reports whether two sides differ enough to flag (design D6): the probe
// fired in exactly one run, or both fired but the count ratio is ≥10×.
func divergent(a, b DiffSide) bool {
	if a.Fired != b.Fired {
		return true
	}
	if !a.Fired { // neither fired (only possible via an explicit empty side)
		return false
	}
	hi, lo := a.Total, b.Total
	if lo > hi {
		hi, lo = lo, hi
	}
	if lo == 0 {
		return hi > 0 // one-sided already handled, but keep the guard explicit
	}
	return hi >= lo*divergenceRatio
}

// runExists reports whether run is a known run of sess.
func runExists(sess *Session, run string) bool {
	for _, r := range sess.Runs {
		if r.ID == run {
			return true
		}
	}
	return false
}
