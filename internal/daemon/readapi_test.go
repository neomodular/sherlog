package daemon

import (
	"net/http"
	"testing"

	"github.com/neomodular/sherlog/internal/store"
)

// seedRun opens a run on the session, ingests one hit for each given probe through
// the public endpoint (so the latest-open-run attribution path is exercised), then
// closes the run with the verdict. Returns the run ID.
func seedRun(t *testing.T, srv *Server, st *store.Store, sessionID string, verdict store.RunVerdict, probes ...string) string {
	t.Helper()
	run, err := st.OpenRun(sessionID)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	for _, p := range probes {
		if w := do(srv, http.MethodPost, "/log/"+sessionID+"/"+p, `{"i":1}`); w.Code != http.StatusOK {
			t.Fatalf("ingest %s: status %d", p, w.Code)
		}
	}
	if _, err := st.CloseRun(sessionID, run.ID, verdict); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	return run.ID
}

// TestDiffEndpoint covers GET /api/sessions/<id>/diff?a=&b= (log-query spec: Run
// diff). Probe p3 fires only in the fixed-check run, so it must be flagged
// divergent; a probe firing in both runs reports both sides and is not flagged.
func TestDiffEndpoint(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	runA := seedRun(t, srv, st, sess.ID, store.VerdictReproduced, "p1", "p1") // p1 only
	runB := seedRun(t, srv, st, sess.ID, store.VerdictFixedCheck, "p1", "p3") // p1 + p3

	w := do(srv, http.MethodGet, "/api/sessions/"+sess.ID+"/diff?a="+runA+"&b="+runB, "")
	var diff store.RunDiff
	decode(t, w, &diff)

	if diff.RunA != runA || diff.RunB != runB {
		t.Errorf("diff runs = %s/%s, want %s/%s", diff.RunA, diff.RunB, runA, runB)
	}
	byProbe := map[string]store.ProbeDiff{}
	for _, pd := range diff.Probes {
		byProbe[pd.Probe] = pd
	}
	p3, ok := byProbe["p3"]
	if !ok || !p3.Divergent || p3.A.Fired || !p3.B.Fired {
		t.Errorf("p3 should be divergent, fired only in B: %+v", p3)
	}
	p1, ok := byProbe["p1"]
	if !ok || !p1.A.Fired || !p1.B.Fired {
		t.Errorf("p1 should report both sides fired: %+v", p1)
	}
	// Divergent probes pinned first (design D6).
	if len(diff.Probes) == 0 || !diff.Probes[0].Divergent {
		t.Errorf("divergent probe not pinned to top: %+v", diff.Probes)
	}
}

// TestDiffEndpointInvalidPairs covers the rejection paths (log-query spec: Invalid
// run pair): missing params, an unknown run, and the same run twice are all 400 —
// client request errors the picker can surface, not server faults.
func TestDiffEndpointInvalidPairs(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	runA := seedRun(t, srv, st, sess.ID, store.VerdictReproduced, "p1")

	cases := []struct{ name, query string }{
		{"missing b", "?a=" + runA},
		{"missing both", ""},
		{"unknown run", "?a=" + runA + "&b=r99"},
		{"same run", "?a=" + runA + "&b=" + runA},
	}
	for _, c := range cases {
		w := do(srv, http.MethodGet, "/api/sessions/"+sess.ID+"/diff"+c.query, "")
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status = %d, want 400", c.name, w.Code)
		}
	}

	// Unknown session is a 404, distinct from a malformed request.
	w := do(srv, http.MethodGet, "/api/sessions/zzzzzzzz/diff?a=r1&b=r2", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("unknown session diff = %d, want 404", w.Code)
	}
}

// TestCasesEndpoint covers GET /api/cases (case-board-ui spec: case list): all
// sessions are returned, open before closed, and a closed-solved case carries its
// resolution.
func TestCasesEndpoint(t *testing.T) {
	srv, st := newTestServer(t)
	open := mustSession(t, st)
	solved := mustSession(t, st)
	if _, err := st.CloseSessionWithResolution(solved.ID, &store.Resolution{
		RootCause:  "float rounding in discount calc",
		FixSummary: "round once at the boundary",
	}); err != nil {
		t.Fatalf("close with resolution: %v", err)
	}

	w := do(srv, http.MethodGet, "/api/cases", "")
	var cases []store.Session
	decode(t, w, &cases)

	if len(cases) != 2 {
		t.Fatalf("want 2 cases, got %d", len(cases))
	}
	// Open first (design: open before closed).
	if cases[0].ID != open.ID || cases[0].ClosedAt != nil {
		t.Errorf("first case should be the open one, got %+v", cases[0])
	}
	if cases[1].ID != solved.ID || cases[1].Resolution == nil {
		t.Fatalf("closed case missing resolution: %+v", cases[1])
	}
	if cases[1].Resolution.RootCause != "float rounding in discount calc" {
		t.Errorf("resolution not surfaced: %+v", cases[1].Resolution)
	}
}

// TestStaleProbesEndpoint covers GET /api/probes/stale (case-board-ui spec: stale
// probes view): an unremoved probe in any session is listed; a removed one is not.
func TestStaleProbesEndpoint(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	if _, err := st.RegisterProbe(sess.ID, store.Probe{ID: "p1", File: "a.js", Line: 5}); err != nil {
		t.Fatalf("RegisterProbe p1: %v", err)
	}
	if _, err := st.RegisterProbe(sess.ID, store.Probe{ID: "p2", File: "b.js", Line: 9}); err != nil {
		t.Fatalf("RegisterProbe p2: %v", err)
	}
	if err := st.RemoveProbe(sess.ID, "p2"); err != nil {
		t.Fatalf("RemoveProbe p2: %v", err)
	}

	w := do(srv, http.MethodGet, "/api/probes/stale", "")
	var stale []store.StaleProbe
	decode(t, w, &stale)

	if len(stale) != 1 || stale[0].Probe.ID != "p1" {
		t.Fatalf("want only the unremoved p1, got %+v", stale)
	}
	if stale[0].SessionID != sess.ID || stale[0].Probe.File != "a.js" {
		t.Errorf("stale probe missing locating info: %+v", stale[0])
	}
}

// TestBrowserRoutesGETOnly is the read-only guarantee guard (case-board-ui spec:
// Read-only UI): every browser-facing route rejects non-GET verbs, so the UI can
// never expose a mutation surface. Mutations stay exclusive to the MCP/internal
// path (design D2).
func TestBrowserRoutesGETOnly(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	routes := []string{
		"/api/cases",
		"/api/probes/stale",
		"/api/events?session=" + sess.ID,
		"/api/sessions/" + sess.ID + "/diff?a=r1&b=r2",
	}
	// Verbs that must never be honored on a read-only route.
	mutating := []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete}

	for _, route := range routes {
		for _, m := range mutating {
			w := do(srv, m, route, "")
			if w.Code != http.StatusMethodNotAllowed {
				t.Errorf("%s %s = %d, want 405 (read-only route)", m, route, w.Code)
			}
		}
	}
}
