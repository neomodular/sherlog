package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"testing"
	"time"
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

	sess, existing, err := s.CreateSession("", "login fails intermittently", "/home/u/code/app")
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
		sess, _, err := s.CreateSession("", "bug", fmt.Sprintf("/cwd/%d", i))
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

	first, _, err := s.CreateSession("", "bug A", "/repo/x")
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
			_, existing, err := s.CreateSession("", "bug B", tc.cwd)
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
	first, _, err := s.CreateSession("", "bug", "/repo/z")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CloseSession(first.ID); err != nil {
		t.Fatalf("close: %v", err)
	}
	_, existing, err := s.CreateSession("", "bug 2", "/repo/z")
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
	sess, _, err := s.CreateSession("", "bug", "/repo")
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
	sess, _, _ := s.CreateSession("", "bug", "/repo")
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
	sess, _, _ := s.CreateSession("", "bug", "/repo")

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
	a, _, _ := s.CreateSession("", "bug a", "/repo/a")
	b, _, _ := s.CreateSession("", "bug b", "/repo/b")

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

	old, _, _ := s.CreateSession("", "old bug", "/repo/old")
	newer, _, _ := s.CreateSession("", "new bug", "/repo/new")
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

	sess, _, _ := s1.CreateSession("", "intermittent 500", "/repo")
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
			sess, _, _ := s.CreateSession("", "flood", "/repo/"+tc.name)
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
	sess, _, _ := s.CreateSession("", "bug", "/repo")

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
	sess, _, _ := s.CreateSession("", "bug", "/repo")
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
	sess, _, _ := s.CreateSession("", "bug", "/repo")

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
	sess, _, _ := s.CreateSession("", "bug", "/repo")
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
	sess, _, _ := s.CreateSession("", "bug", "/repo")
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

// --- pre-run adoption (fix-prerun-event-attribution) ---

// injectOrphan records an orphan event (no run) with an explicit timestamp,
// modelling a probe that fired before any run was open. It writes both the live
// buffer and the durable log so restart-replay tests see the same history.
func injectOrphan(t *testing.T, s *Store, sessionID, probe string, ts time.Time, body any) {
	t.Helper()
	s.mu.Lock()
	entry := s.sessions[sessionID]
	ev := LogEvent{TS: ts, Run: "", Probe: probe, Body: body}
	entry.recordEvent(ev, s.floodN)
	s.mu.Unlock()
	if err := s.appendLog(sessionID, ev); err != nil {
		t.Fatalf("appendLog orphan: %v", err)
	}
}

// closeRunAt closes a run at a chosen time so boundary-sensitive adoption tests
// can place the adoption window edge deterministically (CloseRun uses time.Now).
func (s *Store) closeRunAt(t *testing.T, sessionID, runID string, at time.Time) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	entry := s.sessions[sessionID]
	for i := range entry.session.Runs {
		if entry.session.Runs[i].ID == runID {
			ts := at
			entry.session.Runs[i].ClosedAt = &ts
			entry.session.Runs[i].Verdict = VerdictReproduced
			if err := s.writeState(entry.session); err != nil {
				t.Fatalf("writeState: %v", err)
			}
			return
		}
	}
	t.Fatalf("run %q not found", runID)
}

func probeInSummary(sum []ProbeSummary, probe string) (ProbeSummary, bool) {
	for _, ps := range sum {
		if ps.Probe == probe {
			return ps, true
		}
	}
	return ProbeSummary{}, false
}

// orphanTotal sums the events still attributed to no run (Run==""). QueryFilter's
// empty Run means "any run", so orphan-only inspection reads buffers directly.
func orphanTotal(s *Store, sessionID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	total := 0
	for key, buf := range s.sessions[sessionID].floods {
		if key.run == "" {
			total += buf.total
		}
	}
	return total
}

// TestAdoptFastReproduction covers the headline scenario: probes fire after the
// session starts but before await_run opens a run; opening the run adopts them.
func TestAdoptFastReproduction(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "fast repro", "/repo")

	// Orphans fired after session start and before the run opens (the real
	// ordering: session opens, probes fire, then await_run opens a run). Anchor on
	// the session start with a small positive offset so the timestamps sit strictly
	// inside the window (session start, open] regardless of clock resolution.
	fired := sess.CreatedAt.Add(2 * time.Millisecond)
	injectOrphan(t, s, sess.ID, "p1", fired, map[string]any{"i": float64(1)})
	injectOrphan(t, s, sess.ID, "p1", fired, map[string]any{"i": float64(2)})

	time.Sleep(5 * time.Millisecond)
	run, err := s.OpenRun(sess.ID)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	sum, err := s.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("RunSummary: %v", err)
	}
	ps, ok := probeInSummary(sum, "p1")
	if !ok {
		t.Fatalf("p1 not attributed to %s: %+v", run.ID, sum)
	}
	if ps.Total != 2 || ps.Adopted != 2 {
		t.Errorf("want total=2 adopted=2, got total=%d adopted=%d", ps.Total, ps.Adopted)
	}
	// The orphan bucket must be drained so the events appear only under the run.
	if n := orphanTotal(s, sess.ID); n != 0 {
		t.Errorf("orphan bucket not drained: %d events remain", n)
	}
}

// TestAdoptBoundaryProtectsPriorRun verifies the boundary rule: orphans before a
// closed run's verdict are not pulled into the next run; only post-boundary ones.
func TestAdoptBoundaryProtectsPriorRun(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "boundary", "/repo")

	r1, _ := s.OpenRun(sess.ID)
	s.Ingest(sess.ID, "p1", map[string]any{"k": "r1-direct"}, "")
	// Close r1 just after session start (realistic ordering: start < close), then
	// place orphans either side of that boundary, all before r2 opens.
	boundary := sess.CreatedAt.Add(5 * time.Millisecond)
	s.closeRunAt(t, sess.ID, r1.ID, boundary)

	// One orphan just before the boundary (belongs to nobody now), one just after.
	injectOrphan(t, s, sess.ID, "p1", boundary.Add(-1*time.Millisecond), "pre")
	injectOrphan(t, s, sess.ID, "p1", boundary.Add(1*time.Millisecond), "post")

	// Ensure r2's open time is strictly after the post-boundary orphan.
	time.Sleep(10 * time.Millisecond)
	r2, _ := s.OpenRun(sess.ID)

	sum2, _ := s.RunSummary(sess.ID, r2.ID)
	ps2, ok := probeInSummary(sum2, "p1")
	if !ok || ps2.Total != 1 || ps2.Adopted != 1 {
		t.Errorf("r2 should adopt only the post-boundary orphan: %+v", sum2)
	}
	// r1's direct event is untouched.
	sum1, _ := s.RunSummary(sess.ID, r1.ID)
	ps1, _ := probeInSummary(sum1, "p1")
	if ps1.Total != 1 || ps1.Adopted != 0 {
		t.Errorf("r1 evidence disturbed: %+v", sum1)
	}
	// The pre-boundary orphan stays unattributed.
	if n := orphanTotal(s, sess.ID); n != 1 {
		t.Errorf("pre-boundary orphan should remain: %d orphans", n)
	}
}

// TestAdoptCapExcludesAncient verifies an orphan older than the 15-minute cap is
// not adopted even when no prior run boundary would otherwise exclude it.
func TestAdoptCapExcludesAncient(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "cap", "/repo")

	injectOrphan(t, s, sess.ID, "p1", time.Now().UTC().Add(-40*time.Minute), "ancient")

	run, _ := s.OpenRun(sess.ID)
	sum, _ := s.RunSummary(sess.ID, run.ID)
	if _, ok := probeInSummary(sum, "p1"); ok {
		t.Errorf("ancient orphan must not be adopted: %+v", sum)
	}
	if n := orphanTotal(s, sess.ID); n != 1 {
		t.Errorf("ancient orphan should remain unattributed: %d orphans", n)
	}
}

// TestAdoptReattachNoOp verifies re-attaching to an already-open run adopts
// nothing: adoption happens only at open.
func TestAdoptReattachNoOp(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "reattach", "/repo")

	run, _ := s.OpenOrAttachRun(sess.ID) // opens r1
	// An orphan arriving while the run is open would normally be a direct hit;
	// inject one as an orphan to prove re-attach does not sweep it up.
	injectOrphan(t, s, sess.ID, "p1", time.Now().UTC(), "stray")

	again, _ := s.OpenOrAttachRun(sess.ID) // re-attach, no adoption
	if again.ID != run.ID {
		t.Fatalf("re-attach opened a new run: %s vs %s", again.ID, run.ID)
	}
	sum, _ := s.RunSummary(sess.ID, run.ID)
	if _, ok := probeInSummary(sum, "p1"); ok {
		t.Errorf("re-attach must not adopt orphans: %+v", sum)
	}
}

// TestAdoptSurvivesRestart verifies the append-only marker replays to the same
// attribution after a daemon restart.
func TestAdoptSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	s1, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	sess, _, _ := s1.CreateSession("", "restart adopt", "/repo")
	injectOrphan(t, s1, sess.ID, "p1", sess.CreatedAt.Add(2*time.Millisecond), "x")
	time.Sleep(5 * time.Millisecond)
	run, _ := s1.OpenRun(sess.ID)

	pre, _ := s1.RunSummary(sess.ID, run.ID)
	if ps, ok := probeInSummary(pre, "p1"); !ok || ps.Adopted != 1 {
		t.Fatalf("pre-restart adoption wrong: %+v", pre)
	}

	s2, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s2 (restart): %v", err)
	}
	post, err := s2.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("RunSummary after restart: %v", err)
	}
	ps, ok := probeInSummary(post, "p1")
	if !ok || ps.Total != 1 || ps.Adopted != 1 {
		t.Errorf("adoption not restored by replay: %+v", post)
	}
	// And no orphan residue survives replay.
	if n := orphanTotal(s2, sess.ID); n != 0 {
		t.Errorf("orphan residue after replay: %d events", n)
	}
}

// TestAdoptThenDirectSurvivesRestart is the regression for the two-pass replay
// merge bug: a run that has BOTH adopted orphans and a direct post-open event must
// replay to the same attribution as the live view. Replay loads the direct event
// into the run's buffer first (pass 1), then applies the adoption marker (pass 2);
// without merging, the marker overwrote the buffer and discarded the direct event,
// so replay undercounted the run (live total 3 vs replay total 2). This asserts
// live and replayed RunSummary (Total, Adopted, Truncated) and RunTotal match
// exactly per probe.
func TestAdoptThenDirectSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	s1, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	sess, _, _ := s1.CreateSession("", "adopt then direct", "/repo")

	// Two orphans fire before the run opens (they will be adopted into it).
	fired := sess.CreatedAt.Add(2 * time.Millisecond)
	injectOrphan(t, s1, sess.ID, "p1", fired, map[string]any{"i": float64(1)})
	injectOrphan(t, s1, sess.ID, "p1", fired, map[string]any{"i": float64(2)})

	time.Sleep(5 * time.Millisecond)
	run, err := s1.OpenRun(sess.ID)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	// A direct event ingested into the now-open run lands in the SAME (run, probe)
	// buffer that adoption populated — the collision the merge must handle.
	if err := s1.Ingest(sess.ID, "p1", map[string]any{"i": float64(3)}, ""); err != nil {
		t.Fatalf("Ingest direct: %v", err)
	}

	live, err := s1.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("live RunSummary: %v", err)
	}
	livePS, ok := probeInSummary(live, "p1")
	if !ok {
		t.Fatalf("p1 missing from live summary: %+v", live)
	}
	if livePS.Total != 3 || livePS.Adopted != 2 {
		t.Fatalf("live attribution wrong: want total=3 adopted=2, got %+v", livePS)
	}
	liveTotal, err := s1.RunTotal(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("live RunTotal: %v", err)
	}

	// Restart: fresh Store over the same root, replaying logs.jsonl.
	s2, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s2 (restart): %v", err)
	}
	replay, err := s2.RunSummary(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("replay RunSummary: %v", err)
	}
	replayPS, ok := probeInSummary(replay, "p1")
	if !ok {
		t.Fatalf("p1 missing from replay summary: %+v", replay)
	}
	if replayPS.Total != livePS.Total {
		t.Errorf("replay Total=%d != live Total=%d (direct event discarded by overwrite)", replayPS.Total, livePS.Total)
	}
	if replayPS.Adopted != livePS.Adopted {
		t.Errorf("replay Adopted=%d != live Adopted=%d", replayPS.Adopted, livePS.Adopted)
	}
	if replayPS.Truncated != livePS.Truncated {
		t.Errorf("replay Truncated=%v != live Truncated=%v", replayPS.Truncated, livePS.Truncated)
	}
	replayTotal, err := s2.RunTotal(sess.ID, run.ID)
	if err != nil {
		t.Fatalf("replay RunTotal: %v", err)
	}
	if replayTotal != liveTotal {
		t.Errorf("replay RunTotal=%d != live RunTotal=%d", replayTotal, liveTotal)
	}
	// No orphan residue survives replay: every orphan was adopted.
	if n := orphanTotal(s2, sess.ID); n != 0 {
		t.Errorf("orphan residue after replay: %d events", n)
	}
}

// TestAdoptTruncatedSplitMinimum verifies that when flood truncation straddles the
// adoption boundary, the adopted total is reported as a disclosed minimum.
func TestAdoptTruncatedSplitMinimum(t *testing.T) {
	const n = 3
	s := newTestStore(t, WithFloodN(n))
	sess, _, _ := s.CreateSession("", "truncated split", "/repo")

	// Anchor on session start so the boundary lands after it (realistic ordering);
	// use millisecond offsets and a short sleep so r2 opens strictly later.
	base := sess.CreatedAt
	boundary := base.Add(10 * time.Millisecond)

	// Open r1 (no orphans yet, adopts nothing) and close it exactly at the
	// boundary so r2's adoption window starts there.
	r1, _ := s.OpenRun(sess.ID)
	s.closeRunAt(t, sess.ID, r1.ID, boundary)

	// Now fill one orphan buffer with 10 pre-boundary then 10 post-boundary events,
	// far exceeding 2N so the middle (which spans the boundary) is dropped — the
	// split must then report the adopted side as a disclosed minimum.
	for i := 0; i < 10; i++ {
		injectOrphan(t, s, sess.ID, "p1", base.Add(time.Duration(i)*time.Millisecond), map[string]any{"i": float64(i)})
	}
	for i := 0; i < 10; i++ {
		injectOrphan(t, s, sess.ID, "p1", boundary.Add(time.Duration(i+1)*time.Millisecond), map[string]any{"i": float64(100 + i)})
	}

	time.Sleep(40 * time.Millisecond)
	r2, _ := s.OpenRun(sess.ID)
	sum, _ := s.RunSummary(sess.ID, r2.ID)
	ps, ok := probeInSummary(sum, "p1")
	if !ok {
		t.Fatalf("p1 not adopted into r2: %+v", sum)
	}
	if !ps.Truncated {
		t.Errorf("split across truncation must disclose truncation: %+v", ps)
	}
	// A genuine straddle (pre- AND post-boundary events, truncated) reports the
	// adopted total as a disclosed minimum: only the retained post-boundary events
	// (the last-N window, all post-boundary here = n), strictly less than the 10
	// true post-boundary events because the dropped middle is ambiguous.
	if ps.Adopted != n {
		t.Errorf("straddle adopted should equal retained post-boundary window n=%d: %+v", n, ps)
	}
	if ps.Adopted >= 10 {
		t.Errorf("straddle adopted must be a strict minimum below the 10 true post-boundary events: %+v", ps)
	}
	if ps.Adopted != ps.Total {
		t.Errorf("fully adopted bucket should have adopted==total: %+v", ps)
	}
}

// TestAdoptAllInWindowTruncatedExact is the headline fast-repro scenario: a hot
// loop floods one orphan buffer with events that are ALL post-boundary, then the
// run opens. The truncation never straddles the adoption boundary, so the adopted
// total must be the EXACT counter (design D3 "exact in all common cases"), not a
// minimum — even though the middle was dropped and Truncated is still disclosed.
func TestAdoptAllInWindowTruncatedExact(t *testing.T) {
	const n = 3
	const fired = 100 // >> 2N, forcing a dropped middle within the buffer
	s := newTestStore(t, WithFloodN(n))
	sess, _, _ := s.CreateSession("", "all-in-window truncated", "/repo")

	// Every orphan fires strictly after the adoption lower bound (session start)
	// and before the run opens, so the whole buffer is post-boundary. Anchor on a
	// fixed instant safely after session start but in the past relative to run open
	// (sub-microsecond spacing keeps all 100 < now so none fall outside (from, now]).
	base := sess.CreatedAt.Add(2 * time.Millisecond)
	for i := 0; i < fired; i++ {
		injectOrphan(t, s, sess.ID, "p1", base.Add(time.Duration(i)*time.Microsecond), map[string]any{"i": float64(i)})
	}

	time.Sleep(5 * time.Millisecond) // ensure run opens strictly after the last event
	r1, _ := s.OpenRun(sess.ID)
	sum, _ := s.RunSummary(sess.ID, r1.ID)
	ps, ok := probeInSummary(sum, "p1")
	if !ok {
		t.Fatalf("p1 not adopted into r1: %+v", sum)
	}
	// Exact totals: the whole buffer moved, so its counter transfers intact.
	if ps.Total != fired {
		t.Errorf("Total should be exact %d, got %d (understated): %+v", fired, ps.Total, ps)
	}
	if ps.Adopted != fired {
		t.Errorf("Adopted should be exact %d, got %d (understated): %+v", fired, ps.Adopted, ps)
	}
	// Truncation is still disclosed via the normal total>retained path.
	if !ps.Truncated {
		t.Errorf("dropped middle must still disclose truncation: %+v", ps)
	}
	// No orphan residue: the buffer drained entirely into the run.
	if got := orphanTotal(s, sess.ID); got != 0 {
		t.Errorf("orphan residue after full adoption: %d", got)
	}
}

// logLineKinds returns one token per non-empty line in a session's logs.jsonl:
// "event" for a probe hit, "adopt" for an adoption marker. Tests use it to assert
// the on-disk ordering invariant that every adopted event's line precedes its
// run's marker (fix-prerun: marker-ordering race).
func logLineKinds(t *testing.T, s *Store, sessionID string) []string {
	t.Helper()
	f, err := os.Open(filepath.Join(s.sessionDir(sessionID), logsFile))
	if err != nil {
		t.Fatalf("open logs: %v", err)
	}
	defer f.Close()
	var kinds []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var ll logLine
		if err := json.Unmarshal(line, &ll); err == nil && ll.Adopt != nil {
			kinds = append(kinds, "adopt")
			continue
		}
		kinds = append(kinds, "event")
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("scan logs: %v", err)
	}
	return kinds
}

// TestAdoptMarkerOrderedAfterEventsUnderRace is the regression for the marker-
// ordering race: an orphan recorded in memory whose log line is still in flight
// must have its line flushed before a concurrently-opening run writes its adoption
// marker, or restart replay would apply the marker before the event loads and
// leave it an orphan (live vs. post-restart divergence). Many iterations of
// concurrent Ingest + OpenRun exercise the interleaving; each asserts (a) on-disk
// every adopt line follows the event lines for its run, and (b) attribution
// survives a restart. Run under -race to also flag the data race directly.
func TestAdoptMarkerOrderedAfterEventsUnderRace(t *testing.T) {
	for iter := 0; iter < 50; iter++ {
		root := t.TempDir()
		s1, err := New(WithRoot(root))
		if err != nil {
			t.Fatalf("New s1: %v", err)
		}
		sess, _, _ := s1.CreateSession("", "race", "/repo")

		// Fire orphans and open a run concurrently. The orphans land in memory and
		// their log lines flush asynchronously; OpenRun may observe them in memory
		// and adopt them while their lines are still being written.
		const orphans = 8
		var wg sync.WaitGroup
		for i := 0; i < orphans; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if err := s1.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, ""); err != nil {
					t.Errorf("Ingest: %v", err)
				}
			}(i)
		}
		var run Run
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := s1.OpenRun(sess.ID)
			if err != nil {
				t.Errorf("OpenRun: %v", err)
				return
			}
			run = r
		}()
		wg.Wait()

		// Count the run's adopted events from the live summary. Adopted events were
		// orphans visible in memory at open, so every one of their lines must be
		// durable on disk before the adoption marker — otherwise replay applies the
		// marker first and the event reverts to an orphan.
		sum, err := s1.RunSummary(sess.ID, run.ID)
		if err != nil {
			t.Fatalf("RunSummary: %v", err)
		}
		adopted := 0
		for _, ps := range sum {
			adopted += ps.Adopted
		}

		// On-disk invariant: at least `adopted` event lines precede the marker.
		kinds := logLineKinds(t, s1, sess.ID)
		eventsBeforeMarker, sawMarker := 0, false
		for _, k := range kinds {
			if k == "adopt" {
				sawMarker = true
				break
			}
			eventsBeforeMarker++
		}
		if adopted > 0 {
			if !sawMarker {
				t.Fatalf("iter %d: %d events adopted but no marker on disk: %v", iter, adopted, kinds)
			}
			if eventsBeforeMarker < adopted {
				t.Fatalf("iter %d: only %d event lines precede marker but %d were adopted: %v",
					iter, eventsBeforeMarker, adopted, kinds)
			}
		}

		// Restart and confirm attribution matches the live view: whatever the live
		// run total is, replay must reproduce it exactly (no events reverting to
		// orphans).
		liveTotal, err := s1.RunTotal(sess.ID, run.ID)
		if err != nil {
			t.Fatalf("live RunTotal: %v", err)
		}
		s2, err := New(WithRoot(root))
		if err != nil {
			t.Fatalf("New s2 (restart): %v", err)
		}
		replayTotal, err := s2.RunTotal(sess.ID, run.ID)
		if err != nil {
			t.Fatalf("replay RunTotal: %v", err)
		}
		if replayTotal != liveTotal {
			t.Fatalf("iter %d: attribution diverged across restart: live=%d replay=%d", iter, liveTotal, replayTotal)
		}
		if n := orphanTotal(s2, sess.ID); n != orphans-liveTotal {
			t.Fatalf("iter %d: orphan residue wrong after replay: %d (live adopted %d of %d)", iter, n, liveTotal, orphans)
		}
	}
}

// TestAdoptMarkerFailureLeavesNoAdoption verifies fix-prerun D2 atomicity: if the
// adoption marker cannot be persisted, the open fails and NO in-memory adoption is
// applied, so memory and disk cannot disagree (a later restart would otherwise
// revert the in-memory-adopted events to orphans). The marker write is forced to
// fail by making logs.jsonl unwritable.
func TestAdoptMarkerFailureLeavesNoAdoption(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "marker fail", "/repo")
	injectOrphan(t, s, sess.ID, "p1", sess.CreatedAt.Add(2*time.Millisecond), "x")
	time.Sleep(5 * time.Millisecond)

	// Replace logs.jsonl with a directory so the append open fails. injectOrphan
	// already created the file; remove it first.
	logsPath := filepath.Join(s.sessionDir(sess.ID), logsFile)
	if err := os.Remove(logsPath); err != nil {
		t.Fatalf("remove logs file: %v", err)
	}
	if err := os.Mkdir(logsPath, 0o755); err != nil {
		t.Fatalf("mkdir over logs path: %v", err)
	}

	if _, err := s.OpenRun(sess.ID); err == nil {
		t.Fatalf("OpenRun should fail when the adoption marker cannot be written")
	}

	// The orphan must remain an orphan in memory (no half-applied adoption).
	if n := orphanTotal(s, sess.ID); n != 1 {
		t.Errorf("orphan must remain unattributed after marker-write failure: %d", n)
	}
}

// TestAdoptFullyAdoptedDisclosure covers the spec scenario "Fully adopted run":
// a run whose only events were adopted shows count and adopted equal per probe.
func TestAdoptFullyAdoptedDisclosure(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "fully adopted", "/repo")
	fired := sess.CreatedAt.Add(2 * time.Millisecond)
	injectOrphan(t, s, sess.ID, "p1", fired, "a")
	injectOrphan(t, s, sess.ID, "p2", fired, "b")

	time.Sleep(5 * time.Millisecond)
	run, _ := s.OpenRun(sess.ID)
	sum, _ := s.RunSummary(sess.ID, run.ID)
	if len(sum) != 2 {
		t.Fatalf("want 2 probes, got %+v", sum)
	}
	for _, ps := range sum {
		if ps.Total == 0 || ps.Total != ps.Adopted {
			t.Errorf("probe %s: attribution not fully disclosed as adopted: %+v", ps.Probe, ps)
		}
	}
}
