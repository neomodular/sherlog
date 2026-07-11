package daemon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// The detective-loop gates (harden-detective-gates) are enforced in the store; these
// tests assert the daemon's API surface passes the new params through and maps the
// typed store errors to actionable 4xx responses with the repair instruction intact
// (D-K), plus the daemon-owned probe location check (D-G) and the computed-payload
// additions (commit, repro rate, run predictions, resolution refs).

// sessionWithCWD creates a session rooted at a real temp dir so the probe location
// check (D-G) has a real cwd to resolve against. Returns the session and its cwd.
// The temp dir is never the real repo (house rule: daemon tests use a temp cwd).
func sessionWithCWD(t *testing.T, st *store.Store) (*store.Session, string) {
	t.Helper()
	cwd := t.TempDir()
	sess, _, err := st.CreateSession("", "bug", cwd)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess, cwd
}

// writeLines writes a file of n numbered lines under dir and returns its base name.
func writeLines(t *testing.T, dir, name string, n int) string {
	t.Helper()
	var b strings.Builder
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "line %d\n", i)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return name
}

// probesOf reads a session's registered probes back through the daemon's session
// detail endpoint (which embeds the session, so a bare Session decodes cleanly).
func probesOf(t *testing.T, srv *Server, id string) []store.Probe {
	t.Helper()
	w := do(srv, http.MethodGet, "/api/sessions/"+id, "")
	var sess store.Session
	decode(t, w, &sess)
	return sess.Probes
}

// awaitWithPrediction posts an await carrying a fix prediction (D-D) and decodes the
// result. Unlike awaitCall it forwards the prediction param the daemon stamps on the
// run at receipt.
func awaitWithPrediction(t *testing.T, srv *Server, id string, timeoutS int, prediction string) awaitResult {
	t.Helper()
	body, _ := json.Marshal(map[string]any{"timeout_s": timeoutS, "prediction": prediction})
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+id+"/await", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("await status = %d, body = %s", w.Code, w.Body.String())
	}
	var res awaitResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode await: %v", err)
	}
	return res
}

// --- register_probe: location check (D-G) + prediction pair (D-A) through HTTP ---

// TestRegisterProbeLocationCheck covers the daemon-owned location check (mcp-server
// spec: register_probe verifies the probe location). A missing file or an
// out-of-range line is a 400 naming the resolved path, and no probe is registered; a
// valid predicted probe registers and echoes its predictions; an absolute path is
// used as-is.
func TestRegisterProbeLocationCheck(t *testing.T) {
	srv, st := newTestServer(t)
	sess, cwd := sessionWithCWD(t, st)
	writeLines(t, cwd, "app.js", 120) // a real 120-line source file under the session cwd

	// An absolute path to a real file elsewhere passes through unchanged (D-G).
	absDir := t.TempDir()
	writeLines(t, absDir, "abs.js", 8)
	absFile := filepath.Join(absDir, "abs.js")

	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantInBody string // substring the error/response must contain
		registers  bool   // whether a probe should end up registered
	}{
		{
			name:       "nonexistent file rejected",
			body:       `{"id":"p1","file":"src/ghost.js","line":3,"hypothesis_id":"h1"}`,
			wantStatus: http.StatusBadRequest,
			wantInBody: filepath.Join(cwd, "src/ghost.js"), // resolved path named
		},
		{
			name:       "line beyond end rejected",
			body:       `{"id":"p1","file":"app.js","line":900,"hypothesis_id":"h1"}`,
			wantStatus: http.StatusBadRequest,
			wantInBody: "120 lines", // the file's true line count is named
		},
		{
			name:       "line zero rejected",
			body:       `{"id":"p1","file":"app.js","line":0,"hypothesis_id":"h1"}`,
			wantStatus: http.StatusBadRequest,
			wantInBody: "out of range",
		},
		{
			name:       "valid predicted probe registers",
			body:       `{"id":"p1","file":"app.js","line":42,"hypothesis_id":"h1","expected_if_true":"token=null past TTL","expected_if_false":"token fresh"}`,
			wantStatus: http.StatusOK,
			wantInBody: "token=null past TTL", // response echoes the predictions
			registers:  true,
		},
		{
			name:       "absolute path passes through",
			body:       fmt.Sprintf(`{"id":"p2","file":%q,"line":4,"hypothesis_id":"h1"}`, absFile),
			wantStatus: http.StatusOK,
			registers:  true,
		},
	}

	registered := 0
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := len(probesOf(t, srv, sess.ID))
			w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/probes", c.body)
			if w.Code != c.wantStatus {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, c.wantStatus, w.Body.String())
			}
			if c.wantInBody != "" && !strings.Contains(w.Body.String(), c.wantInBody) {
				t.Errorf("body %q does not contain %q", w.Body.String(), c.wantInBody)
			}
			after := len(probesOf(t, srv, sess.ID))
			if c.registers {
				registered++
				if after != before+1 {
					t.Errorf("expected the probe to register (probes %d → %d)", before, after)
				}
			} else if after != before {
				t.Errorf("rejected registration leaked a probe (probes %d → %d)", before, after)
			}
		})
	}
	if registered == 0 {
		t.Fatal("no valid registration exercised the success path")
	}
}

// TestRegisterProbeDirectoryRejected covers the directory-as-file guard: a resolved
// path that is a directory is not a source file and is rejected (D-G).
func TestRegisterProbeDirectoryRejected(t *testing.T) {
	srv, st := newTestServer(t)
	sess, cwd := sessionWithCWD(t, st)
	if err := os.Mkdir(filepath.Join(cwd, "pkg"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/probes",
		`{"id":"p1","file":"pkg","line":1,"hypothesis_id":"h1"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "directory") {
		t.Errorf("error should name the directory: %s", w.Body.String())
	}
}

// TestRegisterProbePredictionPairThroughHTTP covers the store's prediction-pair gate
// (D-A) surfacing as a 400 through the daemon. A malformed pair is rejected even when
// the location is valid; the store's repair instruction is surfaced verbatim.
func TestRegisterProbePredictionPairThroughHTTP(t *testing.T) {
	srv, st := newTestServer(t)
	sess, cwd := sessionWithCWD(t, st)
	writeLines(t, cwd, "app.js", 50)

	cases := []struct {
		name string
		body string
	}{
		{"one-sided", `{"id":"p1","file":"app.js","line":5,"hypothesis_id":"h1","expected_if_true":"only true"}`},
		{"equal pair", `{"id":"p1","file":"app.js","line":5,"hypothesis_id":"h1","expected_if_true":"  Same ","expected_if_false":"same"}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/probes", c.body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
			if len(probesOf(t, srv, sess.ID)) != 0 {
				t.Error("rejected registration leaked a probe")
			}
		})
	}
}

// --- update_hypothesis: evidence citations (D-B) + confirm gate (D-C) through HTTP ---

// TestUpdateHypothesisCitationThroughHTTP drives the citation gate over HTTP: kills
// and confirms require a probe_id/run_id the store cross-checks, and each rejection
// surfaces the store's repair instruction verbatim. The status mapping is asserted
// too (gate rejections 400; a citation to an absent probe/run 404).
func TestUpdateHypothesisCitationThroughHTTP(t *testing.T) {
	newSession := func(t *testing.T) (*Server, *store.Store, *store.Session) {
		srv, st := newTestServer(t)
		sess := mustSession(t, st)
		if _, err := st.SetHypotheses(sess.ID, []string{"cause a", "cause b", "cause c"}); err != nil {
			t.Fatalf("SetHypotheses: %v", err)
		}
		// Store-level registration skips the daemon location check; the predicted probe
		// is what the confirm gate (D-C) requires.
		if _, err := st.RegisterProbe(sess.ID, store.Probe{
			ID: "p1", File: "a.js", Line: 1, HypothesisID: "h1",
			ExpectedIfTrue: "fires past TTL", ExpectedIfFalse: "never fires",
		}); err != nil {
			t.Fatalf("RegisterProbe: %v", err)
		}
		return srv, st, sess
	}

	patch := func(srv *Server, id, hid, body string) *httptest.ResponseRecorder {
		return do(srv, http.MethodPatch, "/api/sessions/"+id+"/hypotheses/"+hid, body)
	}

	t.Run("kill without citation rejected", func(t *testing.T) {
		srv, _, sess := newSession(t)
		w := patch(srv, sess.ID, "h1", `{"status":"killed","note":"no fire"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "evidence citation required") {
			t.Errorf("missing repair instruction: %s", w.Body.String())
		}
	})

	t.Run("kill citing unknown run is 404", func(t *testing.T) {
		srv, _, sess := newSession(t)
		w := patch(srv, sess.ID, "h1", `{"status":"killed","note":"x","probe_id":"p1","run_id":"r9"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "r9") {
			t.Errorf("error should name the unknown run: %s", w.Body.String())
		}
	})

	t.Run("kill citing open run rejected", func(t *testing.T) {
		srv, st, sess := newSession(t)
		if _, err := st.OpenRun(sess.ID); err != nil { // r1 open, no verdict
			t.Fatalf("OpenRun: %v", err)
		}
		w := patch(srv, sess.ID, "h1", `{"status":"killed","note":"x","probe_id":"p1","run_id":"r1"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "verdict") {
			t.Errorf("error should demand a recorded verdict first: %s", w.Body.String())
		}
	})

	t.Run("kill citing a zero-fired closed run accepted", func(t *testing.T) {
		srv, st, sess := newSession(t)
		run := seedRun(t, srv, st, sess.ID, store.VerdictNotReproduced) // p1 fires zero times
		w := patch(srv, sess.ID, "h1",
			fmt.Sprintf(`{"status":"killed","note":"never fired","probe_id":"p1","run_id":%q}`, run))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var h store.Hypothesis
		decode(t, w, &h)
		if h.Status != store.HypothesisKilled || h.EvidenceProbeID != "p1" || h.EvidenceRunID != run {
			t.Errorf("citation not persisted on kill: %+v", h)
		}
	})

	t.Run("confirm without a reproduced run rejected", func(t *testing.T) {
		srv, st, sess := newSession(t)
		run := seedRun(t, srv, st, sess.ID, store.VerdictNotReproduced)
		w := patch(srv, sess.ID, "h1",
			fmt.Sprintf(`{"status":"confirmed","note":"root cause","probe_id":"p1","run_id":%q}`, run))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "never reproduced") {
			t.Errorf("error should explain the no-reproduced-run gate: %s", w.Body.String())
		}
	})

	t.Run("confirm citing an unpredicted probe rejected", func(t *testing.T) {
		srv, st, sess := newSession(t)
		// A prediction-less probe cannot be the confirming probe (D-C).
		if _, err := st.RegisterProbe(sess.ID, store.Probe{ID: "p2", File: "b.js", Line: 2, HypothesisID: "h1"}); err != nil {
			t.Fatalf("RegisterProbe p2: %v", err)
		}
		run := seedRun(t, srv, st, sess.ID, store.VerdictReproduced)
		w := patch(srv, sess.ID, "h1",
			fmt.Sprintf(`{"status":"confirmed","note":"root cause","probe_id":"p2","run_id":%q}`, run))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "expected_if_true") {
			t.Errorf("error should demand a predicted probe: %s", w.Body.String())
		}
	})

	t.Run("fully qualified confirm succeeds", func(t *testing.T) {
		srv, st, sess := newSession(t)
		run := seedRun(t, srv, st, sess.ID, store.VerdictReproduced)
		w := patch(srv, sess.ID, "h1",
			fmt.Sprintf(`{"status":"confirmed","note":"root cause","probe_id":"p1","run_id":%q}`, run))
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		var h store.Hypothesis
		decode(t, w, &h)
		if h.Status != store.HypothesisConfirmed || h.EvidenceProbeID != "p1" {
			t.Errorf("confirm citation not persisted: %+v", h)
		}
	})

	t.Run("refine to active needs no citation", func(t *testing.T) {
		srv, _, sess := newSession(t)
		w := patch(srv, sess.ID, "h1", `{"status":"active","note":"still open"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
	})
}

// --- set_hypotheses: board minimum (D-E) through HTTP ---

// TestSetHypothesesMinBoardThroughHTTP covers the min-board gate at the API surface:
// a board of two is a 400 and the existing board is unchanged; a board of three
// registers.
func TestSetHypothesesMinBoardThroughHTTP(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	w := do(srv, http.MethodPut, "/api/sessions/"+sess.ID+"/hypotheses", `{"statements":["only","two"]}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("two-suspect board status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "three suspects") {
		t.Errorf("error should name the three-suspect floor: %s", w.Body.String())
	}
	// Board unchanged: still empty.
	if got, err := st.GetSession(sess.ID); err != nil || len(got.Hypotheses) != 0 {
		t.Errorf("rejected board mutated state: %+v (err=%v)", got.Hypotheses, err)
	}

	w = do(srv, http.MethodPut, "/api/sessions/"+sess.ID+"/hypotheses", `{"statements":["a","b","c"]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("three-suspect board status = %d, want 200", w.Code)
	}
}

// --- await + close_run: fix prediction (D-D) and repro rate (D-I) through HTTP ---

// TestAwaitStampsPredictionAndFixedCheckCloses covers the fix-prediction round trip
// over HTTP: await with a prediction stamps it on the run before the summary returns,
// and only then does a fixed-check close succeed.
func TestAwaitStampsPredictionAndFixedCheckCloses(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	res := awaitWithPrediction(t, srv, sess.ID, 1, "p1 now populated; p5 fires zero times")
	if res.Run.Prediction != "p1 now populated; p5 fires zero times" {
		t.Fatalf("await did not stamp the prediction on the run: %+v", res.Run)
	}
	if res.Run.PredictionAt == nil {
		t.Error("prediction stamped with no timestamp")
	}

	// Verify the daemon persisted it before returning by reading the run back.
	got, err := st.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if len(got.Runs) != 1 || got.Runs[0].Prediction == "" {
		t.Fatalf("prediction not persisted on the run: %+v", got.Runs)
	}

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/runs/close", `{"verdict":"fixed-check"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("fixed-check close status = %d, want 200 (body=%s)", w.Code, w.Body.String())
	}
}

// TestFixedCheckWithoutPredictionRejected covers the close-side gate: a fixed-check
// verdict on a predictionless run is a 400 whose message instructs a re-await with a
// prediction, and the run stays open.
func TestFixedCheckWithoutPredictionRejected(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	awaitCall(t, srv, sess.ID, 1) // opens r1 with no prediction

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/runs/close", `{"verdict":"fixed-check"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "re-await with a prediction") {
		t.Errorf("error should instruct a re-await with a prediction: %s", w.Body.String())
	}
	// The run stays open so a re-await can supply the prediction.
	if got, err := st.GetSession(sess.ID); err != nil || got.Runs[0].ClosedAt != nil {
		t.Errorf("rejected fixed-check closed the run: %+v (err=%v)", got.Runs, err)
	}
}

// TestAwaitReportsReproRate covers the repro-rate reporting on the await result
// (mcp-server spec: Repro rate reported). Two reproduced and one not-reproduced
// closed run yield 2/3 with the raw counts; the freshly opened await run is still
// open and excluded from the denominator (D-I).
func TestAwaitReportsReproRate(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	seedRun(t, srv, st, sess.ID, store.VerdictReproduced)
	seedRun(t, srv, st, sess.ID, store.VerdictReproduced)
	seedRun(t, srv, st, sess.ID, store.VerdictNotReproduced)

	res := awaitCall(t, srv, sess.ID, 1) // opens a fresh run, excluded from the rate
	if res.ReproRate.Reproduced != 2 || res.ReproRate.NotReproduced != 1 {
		t.Fatalf("repro counts = %d/%d, want 2 reproduced / 1 not-reproduced", res.ReproRate.Reproduced, res.ReproRate.NotReproduced)
	}
	if res.ReproRate.Rate < 0.66 || res.ReproRate.Rate > 0.67 {
		t.Errorf("rate = %v, want ~0.667", res.ReproRate.Rate)
	}
}

// TestResumeCarriesReproRate covers debug_resume returning the repro rate alongside
// the session state (mcp-server spec: debug_resume). The embedded session promotes
// its fields, so both the session and the added repro_rate decode from one payload.
func TestResumeCarriesReproRate(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	seedRun(t, srv, st, sess.ID, store.VerdictReproduced)
	seedRun(t, srv, st, sess.ID, store.VerdictNotReproduced)

	w := do(srv, http.MethodGet, "/api/sessions", "") // resume-latest
	var resumed struct {
		store.Session
		ReproRate store.ReproRate `json:"repro_rate"`
	}
	decode(t, w, &resumed)
	if resumed.ID != sess.ID {
		t.Fatalf("resumed the wrong session: %s", resumed.ID)
	}
	if resumed.ReproRate.Reproduced != 1 || resumed.ReproRate.NotReproduced != 1 {
		t.Errorf("resume repro counts = %+v, want 1/1", resumed.ReproRate)
	}
}

// --- debug_start: pinned commit (D-H) in the create + resume payloads ---

// TestCommitInStartAndResumePayload covers the pinned-commit surfacing (mcp-server
// spec: debug_start records the pinned commit). A deterministic commit resolver is
// injected so the test never depends on the environment being a git repo; the create
// response and the resume payload both carry the SHA.
func TestCommitInStartAndResumePayload(t *testing.T) {
	const sha = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"
	st, err := store.New(store.WithRoot(t.TempDir()), store.WithCommitResolver(func(string) string { return sha }))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test", config.Default())

	w := do(srv, http.MethodPost, "/api/sessions", `{"description":"bug","cwd":"/tmp/app"}`)
	var created struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &created)
	if created.Session.Commit != sha {
		t.Fatalf("create payload commit = %q, want %q", created.Session.Commit, sha)
	}

	w = do(srv, http.MethodGet, "/api/sessions", "") // resume-latest
	var resumed store.Session
	decode(t, w, &resumed)
	if resumed.Commit != sha {
		t.Errorf("resume payload commit = %q, want %q", resumed.Commit, sha)
	}
}

// TestNonRepoCommitOmitted covers the non-repo path (session-state spec: Non-repository
// cwd tolerated): a resolver that yields "" leaves the commit field absent and never
// blocks the session.
func TestNonRepoCommitOmitted(t *testing.T) {
	st, err := store.New(store.WithRoot(t.TempDir()), store.WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test", config.Default())

	w := do(srv, http.MethodPost, "/api/sessions", `{"description":"bug","cwd":"/tmp/app"}`)
	var created struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &created)
	if created.Session.Commit != "" {
		t.Errorf("commit = %q, want empty for a non-repo cwd", created.Session.Commit)
	}
}

// --- debug_end: solved-close contract (D-F) + prevention refs (D-J) through HTTP ---

// TestSolvedCloseThroughHTTP drives the solved-close contract over the DELETE
// endpoint: a fully qualified close records prevention references, and every
// rejection (partial resolution, unconfirmed hypothesis, unknown guardrail type) is a
// 400 that leaves the session open. An empty body still closes unsolved.
func TestSolvedCloseThroughHTTP(t *testing.T) {
	t.Run("solved close with references", func(t *testing.T) {
		srv, st := newTestServer(t)
		sess, confirmed := seedConfirmedSession(t, srv, st)
		body := fmt.Sprintf(`{"root_cause":"stale token past TTL","fix_summary":"refresh before use","confirmed_hypothesis_id":%q,"regression_test_ref":"TestRefreshRace","guardrail":{"type":"lint","ref":"no-floating-refresh"}}`, confirmed)
		w := do(srv, http.MethodDelete, "/api/sessions/"+sess.ID, body)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		// The prevention references surface through the daemon's session-detail payload
		// (task 2.3: payloads expose resolution refs), read back over HTTP.
		w = do(srv, http.MethodGet, "/api/sessions/"+sess.ID, "")
		var got store.Session
		decode(t, w, &got)
		if got.Resolution == nil || got.Resolution.RegressionTestRef != "TestRefreshRace" {
			t.Fatalf("regression test ref not exposed in the payload: %+v", got.Resolution)
		}
		if got.Resolution.Guardrail == nil || got.Resolution.Guardrail.Type != "lint" || got.Resolution.Guardrail.Ref != "no-floating-refresh" {
			t.Errorf("guardrail not exposed in the payload: %+v", got.Resolution.Guardrail)
		}
	})

	t.Run("unconfirmed hypothesis leaves the session open", func(t *testing.T) {
		srv, st := newTestServer(t)
		sess, _ := seedConfirmedSession(t, srv, st)
		// h2 is active, not confirmed.
		body := `{"root_cause":"x","fix_summary":"y","confirmed_hypothesis_id":"h2"}`
		w := do(srv, http.MethodDelete, "/api/sessions/"+sess.ID, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		got, err := st.GetSession(sess.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if got.ClosedAt != nil {
			t.Error("rejected solved close closed the session anyway")
		}
		if got.Resolution != nil {
			t.Error("rejected solved close recorded a resolution")
		}
		if len(got.Hypotheses) != 3 {
			t.Errorf("board not intact after rejection: %+v", got.Hypotheses)
		}
	})

	t.Run("partial resolution rejected", func(t *testing.T) {
		srv, st := newTestServer(t)
		sess, _ := seedConfirmedSession(t, srv, st)
		w := do(srv, http.MethodDelete, "/api/sessions/"+sess.ID, `{"root_cause":"only this"}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if got, _ := st.GetSession(sess.ID); got.ClosedAt != nil {
			t.Error("partial resolution closed the session")
		}
	})

	t.Run("unknown guardrail type rejected", func(t *testing.T) {
		srv, st := newTestServer(t)
		sess, confirmed := seedConfirmedSession(t, srv, st)
		body := fmt.Sprintf(`{"root_cause":"x","fix_summary":"y","confirmed_hypothesis_id":%q,"guardrail":{"type":"vibes","ref":"?"}}`, confirmed)
		w := do(srv, http.MethodDelete, "/api/sessions/"+sess.ID, body)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "test, lint, alert, doc") {
			t.Errorf("error should name the allowed guardrail types: %s", w.Body.String())
		}
		if got, _ := st.GetSession(sess.ID); got.ClosedAt != nil {
			t.Error("invalid guardrail closed the session")
		}
	})

	t.Run("unsolved close unaffected", func(t *testing.T) {
		srv, st := newTestServer(t)
		sess := mustSession(t, st)
		w := do(srv, http.MethodDelete, "/api/sessions/"+sess.ID, "")
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (body=%s)", w.Code, w.Body.String())
		}
		got, err := st.GetSession(sess.ID)
		if err != nil {
			t.Fatalf("GetSession: %v", err)
		}
		if got.ClosedAt == nil {
			t.Error("unsolved close did not close the session")
		}
		if got.Resolution != nil {
			t.Errorf("unsolved close recorded a resolution: %+v", got.Resolution)
		}
	})
}

// TestSessionDetailCarriesReproRate covers the session-detail payload additions: the
// computed repro rate rides GET /api/sessions/<id> (D-I) while the embedded session
// still decodes as a bare Session. A guard against a regression that drops the field.
func TestSessionDetailCarriesReproRate(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	seedRun(t, srv, st, sess.ID, store.VerdictReproduced)

	w := do(srv, http.MethodGet, "/api/sessions/"+sess.ID, "")
	var detail struct {
		store.Session
		ReproRate store.ReproRate `json:"repro_rate"`
	}
	decode(t, w, &detail)
	if detail.ID != sess.ID {
		t.Fatalf("session detail id = %q, want %q", detail.ID, sess.ID)
	}
	if detail.ReproRate.Reproduced != 1 {
		t.Errorf("session detail repro rate = %+v, want 1 reproduced", detail.ReproRate)
	}
}

// --- Hypothesis status enum through HTTP (dogfood regression) ---

// TestInvalidHypothesisStatusThroughHTTP is the release-dogfood blocker
// regression: PATCH with an empty body (or a garbage status) must be a 400 gate
// rejection naming the allowed statuses — never a silent status write.
func TestInvalidHypothesisStatusThroughHTTP(t *testing.T) {
	srv, st, sess := func(t *testing.T) (*Server, *store.Store, *store.Session) {
		srv, st := newTestServer(t)
		sess := mustSession(t, st)
		if _, err := st.SetHypotheses(sess.ID, []string{"a", "b", "c"}); err != nil {
			t.Fatalf("SetHypotheses: %v", err)
		}
		return srv, st, sess
	}(t)

	for name, body := range map[string]string{"empty body": `{}`, "garbage status": `{"status":"garbage"}`} {
		t.Run(name, func(t *testing.T) {
			w := do(srv, http.MethodPatch, "/api/sessions/"+sess.ID+"/hypotheses/h1", body)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "active, killed, confirmed") {
				t.Errorf("error should name the allowed statuses: %s", w.Body.String())
			}
			got, _ := st.GetSession(sess.ID)
			if got.Hypotheses[0].Status != store.HypothesisActive {
				t.Errorf("board mutated by rejected status: %+v", got.Hypotheses[0])
			}
		})
	}
}

// --- Strict /api/ decoding (dogfood regression) ---

// TestUnknownAPIFieldRejected pins the strict decode on /api/ writes: a mistyped
// field (the dogfood hit "predictions" instead of expected_if_true/_false) must
// be a 400, never a silent drop that leaves the client believing state was
// recorded. The public /log/ ingest stays permissive (D3) and is untouched.
func TestUnknownAPIFieldRejected(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	t.Run("register_probe mistyped predictions key", func(t *testing.T) {
		w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/probes",
			`{"id":"p1","file":"a.js","line":1,"hypothesis_id":"h1","predictions":{"true":"x","false":"y"}}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "predictions") {
			t.Errorf("error should name the unknown field: %s", w.Body.String())
		}
	})

	t.Run("set_hypotheses stray field", func(t *testing.T) {
		// The valid key is "statements"; only the stray field may trigger the 400, so
		// this can't pass for the wrong reason (an all-unknown-fields body).
		w := do(srv, http.MethodPut, "/api/sessions/"+sess.ID+"/hypotheses",
			`{"statements":["a","b","c"],"hypothesys":["typo"]}`)
		if w.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want 400 (body=%s)", w.Code, w.Body.String())
		}
		if !strings.Contains(w.Body.String(), "hypothesys") {
			t.Errorf("error should name the stray field: %s", w.Body.String())
		}
	})
}

// --- Cases feed repro rate (dogfood minor) ---

// TestCasesFeedCarriesReproRate pins the list/detail consistency: /api/cases
// serves the same repro_rate envelope the session-detail endpoint carries.
func TestCasesFeedCarriesReproRate(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	if _, err := st.OpenRun(sess.ID); err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if _, err := st.CloseLatestOpenRun(sess.ID, store.VerdictReproduced); err != nil {
		t.Fatalf("CloseLatestOpenRun: %v", err)
	}

	w := do(srv, http.MethodGet, "/api/cases", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d (body=%s)", w.Code, w.Body.String())
	}
	var cases []struct {
		ID        string          `json:"id"`
		ReproRate store.ReproRate `json:"repro_rate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &cases); err != nil {
		t.Fatalf("decode cases feed: %v", err)
	}
	if len(cases) != 1 || cases[0].ID != sess.ID {
		t.Fatalf("unexpected cases feed: %+v", cases)
	}
	if cases[0].ReproRate.Reproduced != 1 {
		t.Errorf("cases feed repro_rate missing: %+v", cases[0].ReproRate)
	}
}
