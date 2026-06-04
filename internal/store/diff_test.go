package store

import (
	"errors"
	"testing"
)

// probeDiff finds a probe in a RunDiff by ID.
func probeDiff(t *testing.T, d RunDiff, probe string) ProbeDiff {
	t.Helper()
	for _, pd := range d.Probes {
		if pd.Probe == probe {
			return pd
		}
	}
	t.Fatalf("probe %q not in diff: %+v", probe, d.Probes)
	return ProbeDiff{}
}

// TestDiffDifferentialDiagnosis covers log-query scenario "Differential diagnosis":
// a probe firing in only one run is flagged divergent with per-run counts and
// samples, while a probe firing in both is reported unflagged.
func TestDiffDifferentialDiagnosis(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("differential", "/repo")

	// Run 1 (reproduced): p1 and p2 fire.
	r1, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", map[string]any{"v": float64(1)}, "")
	s.Ingest(sess.ID, "p2", map[string]any{"v": float64(2)}, "")
	s.CloseRun(sess.ID, r1.ID, VerdictReproduced)

	// Run 2 then run 3 (fixed-check): p1 fires in both, p3 fires only in run 3.
	r2, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", map[string]any{"v": float64(3)}, "")
	s.CloseRun(sess.ID, r2.ID, VerdictNotReproduced)

	r3, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", map[string]any{"v": float64(4)}, "")
	s.Ingest(sess.ID, "p3", map[string]any{"v": float64(5)}, "")
	s.CloseRun(sess.ID, r3.ID, VerdictFixedCheck)

	diff, err := s.DiffRuns(sess.ID, r1.ID, r3.ID)
	if err != nil {
		t.Fatalf("DiffRuns: %v", err)
	}

	// p3 fired only in r3 → divergent, with the run-3 sample present.
	p3 := probeDiff(t, diff, "p3")
	if !p3.Divergent {
		t.Errorf("p3 fired only in one run, should be divergent: %+v", p3)
	}
	if p3.A.Fired || !p3.B.Fired {
		t.Errorf("p3 should be fired only on side B: %+v", p3)
	}
	if p3.B.First == nil || p3.B.Total != 1 {
		t.Errorf("p3 side B should carry its count and sample: %+v", p3.B)
	}

	// p2 fired only in r1 → divergent.
	p2 := probeDiff(t, diff, "p2")
	if !p2.Divergent || !p2.A.Fired || p2.B.Fired {
		t.Errorf("p2 should be divergent, fired only on side A: %+v", p2)
	}

	// p1 fired in both at equal-ish counts → not divergent, both sides reported.
	p1 := probeDiff(t, diff, "p1")
	if p1.Divergent {
		t.Errorf("p1 fired similarly in both runs, should not be divergent: %+v", p1)
	}
	if !p1.A.Fired || !p1.B.Fired || p1.A.Total != 1 || p1.B.Total != 1 {
		t.Errorf("p1 should report both sides: %+v", p1)
	}

	// Divergent probes are pinned to the top.
	for i, pd := range diff.Probes {
		if !pd.Divergent {
			// Once we hit a non-divergent probe, none after it may be divergent.
			for _, rest := range diff.Probes[i:] {
				if rest.Divergent {
					t.Errorf("divergent probe %q not pinned above non-divergent: %+v", rest.Probe, diff.Probes)
				}
			}
			break
		}
	}
}

// TestDiffCountRatioDivergence verifies the ≥10× count-ratio rule: a probe firing
// in both runs but with a large count difference is flagged divergent.
func TestDiffCountRatioDivergence(t *testing.T) {
	s := newTestStore(t, WithFloodN(50))
	sess, _, _ := s.CreateSession("ratio", "/repo")

	r1, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", nil, "x") // 1 hit
	s.Ingest(sess.ID, "p2", nil, "y") // p2: 2 hits in r1
	s.Ingest(sess.ID, "p2", nil, "y")
	s.CloseRun(sess.ID, r1.ID, VerdictReproduced)

	r2, _ := s.OpenRun(sess.ID)
	for i := 0; i < 20; i++ { // p1: 20 hits in r2 → ratio 20× ≥ 10×
		s.Ingest(sess.ID, "p1", nil, "x")
	}
	s.Ingest(sess.ID, "p2", nil, "y") // p2: 1 hit in r2 → ratio 2× < 10×
	s.CloseRun(sess.ID, r2.ID, VerdictNotReproduced)

	diff, err := s.DiffRuns(sess.ID, r1.ID, r2.ID)
	if err != nil {
		t.Fatalf("DiffRuns: %v", err)
	}
	if p1 := probeDiff(t, diff, "p1"); !p1.Divergent {
		t.Errorf("p1 ratio 20× should be divergent: A=%d B=%d", p1.A.Total, p1.B.Total)
	}
	if p2 := probeDiff(t, diff, "p2"); p2.Divergent {
		t.Errorf("p2 ratio 2× should not be divergent: A=%d B=%d", p2.A.Total, p2.B.Total)
	}
}

// TestDiffTruncationDisclosed verifies diff carries the same truncation disclosure
// as a query: a probe whose flood buffer dropped its middle reports Truncated.
func TestDiffTruncationDisclosed(t *testing.T) {
	const n = 3
	s := newTestStore(t, WithFloodN(n))
	sess, _, _ := s.CreateSession("trunc diff", "/repo")

	r1, _ := s.OpenRun(sess.ID)
	for i := 0; i < 100; i++ { // >> 2N, forces a dropped middle
		s.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, "")
	}
	s.CloseRun(sess.ID, r1.ID, VerdictReproduced)

	r2, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", nil, "once")
	s.CloseRun(sess.ID, r2.ID, VerdictNotReproduced)

	diff, err := s.DiffRuns(sess.ID, r1.ID, r2.ID)
	if err != nil {
		t.Fatalf("DiffRuns: %v", err)
	}
	p1 := probeDiff(t, diff, "p1")
	if !p1.A.Truncated {
		t.Errorf("side A flooded probe should disclose truncation: %+v", p1.A)
	}
	if p1.A.Total != 100 {
		t.Errorf("side A total should be the true count 100: %+v", p1.A)
	}
	// First/last samples still present despite truncation.
	if p1.A.First == nil || p1.A.Last == nil {
		t.Errorf("truncated side should still carry first/last samples: %+v", p1.A)
	}
}

// TestDiffInvalidPairs covers log-query scenario "Invalid run pair" plus the other
// rejection cases: cross-session runs, unknown runs, same run, unknown session.
func TestDiffInvalidPairs(t *testing.T) {
	s := newTestStore(t)
	a, _, _ := s.CreateSession("session a", "/repo/a")
	b, _, _ := s.CreateSession("session b", "/repo/b")
	ra1, _ := s.OpenRun(a.ID) // session a: only r1
	rb1, _ := s.OpenRun(b.ID) // session b: r1
	rb2, _ := s.OpenRun(b.ID) // session b: r2 — an ID absent from session a

	// A run from a different session (rb2 = "r2", which session a never opened) is
	// unknown to session a → rejected. This is the cross-session guard: a diff only
	// compares runs that exist in the named session (log-query: invalid run pair).
	if _, err := s.DiffRuns(a.ID, ra1.ID, rb2.ID); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("cross-session run pair: want ErrRunNotFound, got %v", err)
	}
	// An entirely unknown run ID.
	if _, err := s.DiffRuns(a.ID, ra1.ID, "rX"); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("unknown run: want ErrRunNotFound, got %v", err)
	}
	// Same run twice — nothing to compare.
	if _, err := s.DiffRuns(a.ID, ra1.ID, ra1.ID); !errors.Is(err, ErrSameRun) {
		t.Errorf("same run: want ErrSameRun, got %v", err)
	}
	// Unknown session (distinct run IDs so the same-run guard does not fire first).
	if _, err := s.DiffRuns("nosession", rb1.ID, rb2.ID); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("unknown session: want ErrSessionNotFound, got %v", err)
	}
	// A valid same-session pair with no events still succeeds (empty probe set).
	diff, err := s.DiffRuns(b.ID, rb1.ID, rb2.ID)
	if err != nil {
		t.Fatalf("valid empty pair should succeed: %v", err)
	}
	if len(diff.Probes) != 0 {
		t.Errorf("no events fired, expected empty diff: %+v", diff.Probes)
	}
}
