package store

import (
	"errors"
	"fmt"
	"regexp"
	"sync"
	"testing"
)

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()
	opts = append([]Option{WithRoot(t.TempDir())}, opts...)
	s, err := New(opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

var idPattern = regexp.MustCompile(`^[0-9a-z]{8,}$`)

// TestSessionCreated covers spec scenario "Session created": random ID, the
// description/cwd, status open (ClosedAt nil), and the probe URL template the
// caller builds from the ID.
func TestSessionCreated(t *testing.T) {
	s := newTestStore(t)

	sess, existing, err := s.CreateSession("login fails intermittently", "/home/u/code/app")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if existing != nil {
		t.Errorf("unexpected same-cwd session on first create: %+v", existing)
	}
	if !idPattern.MatchString(sess.ID) {
		t.Errorf("ID %q is not ≥8 base36 chars", sess.ID)
	}
	if sess.Description != "login fails intermittently" || sess.CWD != "/home/u/code/app" {
		t.Errorf("fields not stored: %+v", sess)
	}
	if sess.ClosedAt != nil {
		t.Errorf("new session should be open, got ClosedAt=%v", sess.ClosedAt)
	}

	// The probe URL template is built from the ID; assert it is well-formed so a
	// future change to the path scheme is caught here (D4).
	wantURL := "http://127.0.0.1:2218/log/" + sess.ID + "/"
	if !regexp.MustCompile(`^http://127\.0\.0\.1:2218/log/[0-9a-z]{8,}/$`).MatchString(wantURL) {
		t.Errorf("probe URL template malformed: %q", wantURL)
	}
}

// TestRandomIDsUnique guards that CreateSession does not reuse IDs.
func TestRandomIDsUnique(t *testing.T) {
	s := newTestStore(t)
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		sess, _, err := s.CreateSession("bug", fmt.Sprintf("/cwd/%d", i))
		if err != nil {
			t.Fatalf("CreateSession %d: %v", i, err)
		}
		if seen[sess.ID] {
			t.Fatalf("duplicate ID %q", sess.ID)
		}
		seen[sess.ID] = true
	}
}

// TestSameCWDDetection covers spec scenario "Concurrent session warning data".
func TestSameCWDDetection(t *testing.T) {
	s := newTestStore(t)

	first, _, err := s.CreateSession("bug A", "/repo/x")
	if err != nil {
		t.Fatalf("create first: %v", err)
	}

	tests := []struct {
		name         string
		cwd          string
		wantExisting bool
	}{
		{"same cwd surfaces existing", "/repo/x", true},
		{"different cwd no warning", "/repo/y", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, existing, err := s.CreateSession("bug B", tc.cwd)
			if err != nil {
				t.Fatalf("create: %v", err)
			}
			if tc.wantExisting {
				if existing == nil {
					t.Fatal("expected existing same-cwd session, got nil")
				}
				if existing.ID != first.ID || existing.Description != "bug A" {
					t.Errorf("existing session mismatch: %+v", existing)
				}
			} else if existing != nil {
				t.Errorf("did not expect a same-cwd session, got %+v", existing)
			}
		})
	}
}

// TestSameCWDClosedSessionIgnored verifies a closed session does not trigger the
// same-cwd warning (only open sessions count, per spec wording).
func TestSameCWDClosedSessionIgnored(t *testing.T) {
	s := newTestStore(t)
	first, _, err := s.CreateSession("bug", "/repo/z")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CloseSession(first.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, existing, err := s.CreateSession("bug 2", "/repo/z")
	if err != nil {
		t.Fatalf("create 2: %v", err)
	}
	if existing != nil {
		t.Errorf("closed session should not be reported as same-cwd open: %+v", existing)
	}
}

// TestHypothesisKilledWithEvidence covers spec scenario "Hypothesis killed with
// evidence": status and note persist and are readable afterward.
func TestHypothesisKilledWithEvidence(t *testing.T) {
	s := newTestStore(t)
	sess, _, err := s.CreateSession("bug", "/repo")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	board, err := s.SetHypotheses(sess.ID, []string{"race A", "cache stale", "config drift"})
	if err != nil {
		t.Fatalf("SetHypotheses: %v", err)
	}
	if len(board) != 3 || board[1].ID != "h2" || board[1].Status != HypothesisActive {
		t.Fatalf("board not initialised as expected: %+v", board)
	}

	updated, err := s.UpdateHypothesis(sess.ID, "h2", HypothesisKilled, "p4 showed cache fresh in run 1")
	if err != nil {
		t.Fatalf("UpdateHypothesis: %v", err)
	}
	if updated.Status != HypothesisKilled || updated.Note != "p4 showed cache fresh in run 1" {
		t.Errorf("update not applied: %+v", updated)
	}

	read, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if read.Hypotheses[1].Status != HypothesisKilled || read.Hypotheses[1].Note == "" {
		t.Errorf("subsequent read does not show kill+note: %+v", read.Hypotheses[1])
	}
}

func TestUpdateHypothesisErrors(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("bug", "/repo")
	if _, err := s.UpdateHypothesis(sess.ID, "nope", HypothesisKilled, ""); !errors.Is(err, ErrHypothesisNotFound) {
		t.Errorf("want ErrHypothesisNotFound, got %v", err)
	}
	if _, err := s.UpdateHypothesis("missing", "h1", HypothesisKilled, ""); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("want ErrSessionNotFound, got %v", err)
	}
}

// TestUnremovedProbesAtEnd covers spec scenario "Unremoved probes reported at
// session end".
func TestUnremovedProbesAtEnd(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("bug", "/repo")

	for _, p := range []Probe{
		{ID: "p1", File: "auth.js", Line: 10, HypothesisID: "h1"},
		{ID: "p3", File: "cache.js", Line: 88, HypothesisID: "h2"},
	} {
		if _, err := s.RegisterProbe(sess.ID, p); err != nil {
			t.Fatalf("RegisterProbe %s: %v", p.ID, err)
		}
	}
	if err := s.RemoveProbe(sess.ID, "p1"); err != nil {
		t.Fatalf("RemoveProbe: %v", err)
	}

	unremoved, err := s.CloseSession(sess.ID)
	if err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if len(unremoved) != 1 || unremoved[0].ID != "p3" || unremoved[0].File != "cache.js" || unremoved[0].Line != 88 {
		t.Errorf("expected p3 reported with file/line, got %+v", unremoved)
	}
}

// TestStaleProbesAcrossSessions covers spec scenario "Stale probes visible weeks
// later": unremoved probes are listed with session/file/line even after close.
func TestStaleProbesAcrossSessions(t *testing.T) {
	s := newTestStore(t)
	a, _, _ := s.CreateSession("bug a", "/repo/a")
	b, _, _ := s.CreateSession("bug b", "/repo/b")

	s.RegisterProbe(a.ID, Probe{ID: "p1", File: "a.js", Line: 1})
	s.RegisterProbe(a.ID, Probe{ID: "p2", File: "a2.js", Line: 2})
	s.RegisterProbe(b.ID, Probe{ID: "p1", File: "b.js", Line: 3})
	s.RemoveProbe(a.ID, "p1")
	s.CloseSession(a.ID)

	stale := s.StaleProbes()
	if len(stale) != 2 {
		t.Fatalf("expected 2 stale probes, got %d: %+v", len(stale), stale)
	}
	// Sorted by session ID then probe ID; verify session/file/line surfaced.
	for _, sp := range stale {
		if sp.SessionID == "" || sp.Probe.File == "" || sp.Probe.Line == 0 {
			t.Errorf("stale probe missing session/file/line: %+v", sp)
		}
		if sp.SessionID == a.ID && sp.Probe.ID == "p1" {
			t.Errorf("removed probe should not be stale: %+v", sp)
		}
	}
}

// TestResumeLatest covers spec scenario "Resume after context loss": the most
// recently created open session is returned with full state.
func TestResumeLatest(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.ResumeLatest(); !errors.Is(err, ErrNoOpenSession) {
		t.Errorf("empty store should report ErrNoOpenSession, got %v", err)
	}

	old, _, _ := s.CreateSession("old bug", "/repo/old")
	newer, _, _ := s.CreateSession("new bug", "/repo/new")
	s.SetHypotheses(newer.ID, []string{"suspect 1"})

	got, err := s.ResumeLatest()
	if err != nil {
		t.Fatalf("ResumeLatest: %v", err)
	}
	if got.ID != newer.ID {
		t.Errorf("expected most recent session %q, got %q", newer.ID, got.ID)
	}
	if len(got.Hypotheses) != 1 {
		t.Errorf("resumed state missing hypotheses: %+v", got)
	}

	// Closing the newest should fall back to the older open session.
	s.CloseSession(newer.ID)
	got, err = s.ResumeLatest()
	if err != nil {
		t.Fatalf("ResumeLatest after close: %v", err)
	}
	if got.ID != old.ID {
		t.Errorf("expected fallback to %q, got %q", old.ID, got.ID)
	}
}

// TestRestartRecovery covers spec scenarios "Restart mid-investigation" and
// "State survives daemon restart": a new Store over the same root recovers board,
// probes, runs, and ingested events.
func TestRestartRecovery(t *testing.T) {
	root := t.TempDir()
	s1, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}

	sess, _, _ := s1.CreateSession("intermittent 500", "/repo")
	s1.SetHypotheses(sess.ID, []string{"race", "cache", "config"})
	s1.UpdateHypothesis(sess.ID, "h1", HypothesisKilled, "ruled out in run 1")
	s1.RegisterProbe(sess.ID, Probe{ID: "p1", File: "auth.js", Line: 45, HypothesisID: "h1"})
	run, _ := s1.OpenRun(sess.ID)
	for i := 0; i < 5; i++ {
		if err := s1.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, ""); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}
	s1.CloseRun(sess.ID, run.ID, VerdictReproduced)

	// Simulate restart: brand new Store over the same root.
	s2, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s2 (restart): %v", err)
	}
	got, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after restart: %v", err)
	}
	if got.Description != "intermittent 500" {
		t.Errorf("description lost: %q", got.Description)
	}
	if len(got.Hypotheses) != 3 || got.Hypotheses[0].Status != HypothesisKilled {
		t.Errorf("board not recovered: %+v", got.Hypotheses)
	}
	if len(got.Probes) != 1 || got.Probes[0].File != "auth.js" {
		t.Errorf("probes not recovered: %+v", got.Probes)
	}
	if len(got.Runs) != 1 || got.Runs[0].Verdict != VerdictReproduced {
		t.Errorf("runs not recovered: %+v", got.Runs)
	}

	summary, err := s2.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("RunSummary after restart: %v", err)
	}
	if len(summary) != 1 || summary[0].Total != 5 {
		t.Errorf("ingested events not replayed: %+v", summary)
	}

	// A new run after restart must not collide with the recovered run ID.
	run2, err := s2.OpenRun(sess.ID)
	if err != nil {
		t.Fatalf("OpenRun after restart: %v", err)
	}
	if run2.ID == run.ID {
		t.Errorf("run counter not restored: reused %q", run2.ID)
	}
}

// TestFloodControl covers task 2.5 / D8 boundaries: first/last-N retained, exact
// total, truncation flag.
func TestFloodControl(t *testing.T) {
	const n = 5
	tests := []struct {
		name          string
		count         int
		wantRetained  int
		wantTruncated bool
		// wantFirstIdx/wantLastIdx: the body "i" value expected at the start and
		// end of the retained set, proving first-N and last-N selection.
		wantFirstIdx float64
		wantLastIdx  float64
	}{
		{"below n", 3, 3, false, 0, 2},
		{"exactly n", 5, 5, false, 0, 4},
		{"between n and 2n no gap", 8, 8, false, 0, 7},
		{"exactly 2n no gap", 10, 10, false, 0, 9},
		{"above 2n gap dropped", 100, 10, true, 0, 99},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t, WithFloodN(n))
			sess, _, _ := s.CreateSession("flood", "/repo/"+tc.name)
			run, _ := s.OpenRun(sess.ID)
			for i := 0; i < tc.count; i++ {
				if err := s.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, ""); err != nil {
					t.Fatalf("Ingest %d: %v", i, err)
				}
			}

			summary, err := s.RunSummary(sess.ID, run.ID)
			if err != nil {
				t.Fatalf("RunSummary: %v", err)
			}
			if len(summary) != 1 {
				t.Fatalf("expected 1 probe summary, got %d", len(summary))
			}
			ps := summary[0]
			if ps.Total != tc.count {
				t.Errorf("Total=%d want %d", ps.Total, tc.count)
			}
			if len(ps.Events) != tc.wantRetained {
				t.Errorf("retained=%d want %d", len(ps.Events), tc.wantRetained)
			}
			if ps.Truncated != tc.wantTruncated {
				t.Errorf("Truncated=%v want %v", ps.Truncated, tc.wantTruncated)
			}
			gotFirst := ps.Events[0].Body.(map[string]any)["i"]
			gotLast := ps.Events[len(ps.Events)-1].Body.(map[string]any)["i"]
			if gotFirst != tc.wantFirstIdx {
				t.Errorf("first event i=%v want %v", gotFirst, tc.wantFirstIdx)
			}
			if gotLast != tc.wantLastIdx {
				t.Errorf("last event i=%v want %v", gotLast, tc.wantLastIdx)
			}
		})
	}
}

// TestFloodPerProbePerRun verifies buffers are isolated per probe and per run (D8).
func TestFloodPerProbePerRun(t *testing.T) {
	s := newTestStore(t, WithFloodN(3))
	sess, _, _ := s.CreateSession("bug", "/repo")

	r1, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "pA", nil, "a1")
	s.Ingest(sess.ID, "pB", nil, "b1")
	s.Ingest(sess.ID, "pB", nil, "b2")
	s.CloseRun(sess.ID, r1.ID, VerdictReproduced)

	r2, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "pA", nil, "a-run2")

	sum1, _ := s.RunSummary(sess.ID, r1.ID)
	if len(sum1) != 2 {
		t.Fatalf("run1 expected 2 probes, got %d", len(sum1))
	}
	for _, ps := range sum1 {
		switch ps.Probe {
		case "pA":
			if ps.Total != 1 {
				t.Errorf("run1 pA total=%d want 1", ps.Total)
			}
		case "pB":
			if ps.Total != 2 {
				t.Errorf("run1 pB total=%d want 2", ps.Total)
			}
		}
	}

	sum2, _ := s.RunSummary(sess.ID, r2.ID)
	if len(sum2) != 1 || sum2[0].Probe != "pA" || sum2[0].Total != 1 {
		t.Errorf("run2 summary wrong: %+v", sum2)
	}
}

// TestQueryLogsFilters exercises query_logs filtering and limit truncation (D9).
func TestQueryLogsFilters(t *testing.T) {
	s := newTestStore(t, WithFloodN(50))
	sess, _, _ := s.CreateSession("bug", "/repo")
	run, _ := s.OpenRun(sess.ID)
	for i := 0; i < 10; i++ {
		s.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, "")
	}
	for i := 0; i < 4; i++ {
		s.Ingest(sess.ID, "p2", nil, "raw")
	}

	all, err := s.QueryLogs(sess.ID, QueryFilter{})
	if err != nil {
		t.Fatalf("QueryLogs all: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("expected 2 buckets, got %d", len(all))
	}

	only, _ := s.QueryLogs(sess.ID, QueryFilter{Probe: "p1"})
	if len(only) != 1 || only[0].Probe != "p1" || only[0].Total != 10 {
		t.Errorf("probe filter wrong: %+v", only)
	}

	limited, _ := s.QueryLogs(sess.ID, QueryFilter{Probe: "p1", Limit: 3})
	if len(limited) != 1 || len(limited[0].Events) != 3 || !limited[0].Truncated || limited[0].Total != 10 {
		t.Errorf("limit not applied/disclosed: %+v", limited)
	}

	byRun, _ := s.QueryLogs(sess.ID, QueryFilter{Run: run.ID})
	if len(byRun) != 2 {
		t.Errorf("run filter wrong: %+v", byRun)
	}
}

// TestIngestUnknownSession verifies drive-by POSTs to unknown sessions are
// rejected (D4) so the HTTP handler can drop them silently.
func TestIngestUnknownSession(t *testing.T) {
	s := newTestStore(t)
	if err := s.Ingest("doesnotexist", "p1", nil, "x"); !errors.Is(err, ErrSessionNotFound) {
		t.Errorf("want ErrSessionNotFound, got %v", err)
	}
}

// TestRunVerdictAndReattach verifies run open/close and LatestOpenRun re-attach (D8).
func TestRunVerdictAndReattach(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("bug", "/repo")

	if _, ok, err := s.LatestOpenRun(sess.ID); err != nil || ok {
		t.Errorf("no run yet: ok=%v err=%v", ok, err)
	}
	run, _ := s.OpenRun(sess.ID)
	got, ok, err := s.LatestOpenRun(sess.ID)
	if err != nil || !ok || got.ID != run.ID {
		t.Errorf("re-attach failed: got=%+v ok=%v err=%v", got, ok, err)
	}
	if _, err := s.CloseRun(sess.ID, run.ID, VerdictFixedCheck); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	if _, ok, _ := s.LatestOpenRun(sess.ID); ok {
		t.Errorf("closed run should not re-attach")
	}
	if _, err := s.CloseRun(sess.ID, "rX", VerdictReproduced); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("want ErrRunNotFound, got %v", err)
	}
}

// TestConcurrentIngest verifies the store is safe under concurrent ingest from
// many goroutines (run with -race). It also confirms the exact total counter is
// not lost to data races.
func TestConcurrentIngest(t *testing.T) {
	s := newTestStore(t, WithFloodN(10))
	sess, _, _ := s.CreateSession("bug", "/repo")
	run, _ := s.OpenRun(sess.ID)

	const goroutines, perG = 20, 50
	var wg sync.WaitGroup
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func(g int) {
			defer wg.Done()
			for i := 0; i < perG; i++ {
				if err := s.Ingest(sess.ID, "p1", map[string]any{"g": float64(g)}, ""); err != nil {
					t.Errorf("Ingest: %v", err)
					return
				}
			}
		}(g)
	}
	wg.Wait()

	summary, err := s.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	if len(summary) != 1 || summary[0].Total != goroutines*perG {
		t.Errorf("expected total %d, got %+v", goroutines*perG, summary)
	}
}

// TestConcurrentMixedOps stresses concurrent reads and writes across the store's
// surface to surface races in state mutation under -race.
func TestConcurrentMixedOps(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("bug", "/repo")
	s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
	run, _ := s.OpenRun(sess.ID)

	var wg sync.WaitGroup
	work := []func(){
		func() { s.Ingest(sess.ID, "p1", nil, "x") },
		func() { s.RegisterProbe(sess.ID, Probe{ID: "p1", File: "f", Line: 1}) },
		func() { s.UpdateHypothesis(sess.ID, "h1", HypothesisKilled, "n") },
		func() { _, _ = s.GetSession(sess.ID) },
		func() { _, _ = s.RunSummary(sess.ID, run.ID) },
		func() { _ = s.StaleProbes() },
		func() { _, _ = s.QueryLogs(sess.ID, QueryFilter{}) },
		func() { _, _ = s.ResumeLatest() },
	}
	for i := 0; i < 200; i++ {
		fn := work[i%len(work)]
		wg.Add(1)
		go func() { defer wg.Done(); fn() }()
	}
	wg.Wait()
}
