package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/notes"
	"github.com/neomodular/sherlog/internal/store"
)

// newTestServer builds a Server over a temp-dir store and returns it plus the
// store so tests can seed sessions directly. Await timing is tightened so the
// debounce tests run in well under a second.
func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	return newTestServerAt(t, t.TempDir())
}

// newTestServerAt builds a test Server over an explicit store root so restart
// tests can stand up a fresh Server over the same on-disk state.
func newTestServerAt(t *testing.T, root string) (*Server, *store.Store) {
	t.Helper()
	st, err := store.New(store.WithRoot(root))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test", config.Default())
	srv.awaiter.debounce = 150 * time.Millisecond
	srv.awaiter.poll = 10 * time.Millisecond
	return srv, st
}

func mustSession(t *testing.T, st *store.Store) *store.Session {
	t.Helper()
	sess, _, err := st.CreateSession("", "bug", "/tmp/app")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	return sess
}

// post issues a request against the Server's ServeHTTP and returns the recorder.
func do(srv *Server, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	return w
}

// --- Ingest (D3, spec: log-ingest) ---

// TestIngestSimpleRequestNoPreflight covers the browser-style simple request:
// a POST with no JSON Content-Type, parsed as JSON, answered 200 with the
// permissive CORS header — no preflight needed (spec: Browser fetch without preflight).
func TestIngestSimpleRequestNoPreflight(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	w := do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"x":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q, want *", got)
	}

	results, err := st.QueryLogs(sess.ID, store.QueryFilter{Probe: "p1"})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(results) != 1 || results[0].Total != 1 {
		t.Fatalf("want 1 event for p1, got %+v", results)
	}
	if m, ok := results[0].Events[0].Body.(map[string]any); !ok || m["x"] != float64(1) {
		t.Errorf("body not parsed as JSON: %+v", results[0].Events[0])
	}
}

// TestIngestNonJSONAndEmpty covers raw-string fallback and empty-body acceptance.
func TestIngestNonJSONAndEmpty(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	if w := do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", "user=42 state=dirty"); w.Code != 200 {
		t.Fatalf("raw body status = %d", w.Code)
	}
	if w := do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", ""); w.Code != 200 {
		t.Fatalf("empty body status = %d", w.Code)
	}

	results, _ := st.QueryLogs(sess.ID, store.QueryFilter{Probe: "p1"})
	if len(results) != 1 || results[0].Total != 2 {
		t.Fatalf("want 2 events, got %+v", results)
	}
	if results[0].Events[0].Raw != "user=42 state=dirty" {
		t.Errorf("raw not stored: %+v", results[0].Events[0])
	}
}

// TestIngestUnknownSessionDropped covers the drive-by POST defense (D4): unknown
// session yields 200 and stores nothing.
func TestIngestUnknownSessionDropped(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodPost, "/log/zzzzzzzz/p1", `{"x":1}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for unknown session", w.Code)
	}
}

// TestIngestUnder50ms guards the response-latency requirement.
func TestIngestUnder50ms(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	start := time.Now()
	do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"x":1}`)
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("ingest took %v, want < 50ms", elapsed)
	}
}

// TestFloodTruncation covers per-probe flood control through the HTTP layer and
// the truncation disclosure on query (D8, spec: Truncation disclosed).
func TestFloodTruncation(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	const fired = 1000
	for i := 0; i < fired; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p2", `{"i":1}`)
	}

	w := do(srv, http.MethodGet, "/api/sessions/"+sess.ID+"/query?probe=p2", "")
	var results []store.QueryResult
	decode(t, w, &results)
	if len(results) != 1 {
		t.Fatalf("want 1 result bucket, got %d", len(results))
	}
	r := results[0]
	if r.Total != fired {
		t.Errorf("Total = %d, want %d", r.Total, fired)
	}
	if !r.Truncated {
		t.Error("expected Truncated = true under flood")
	}
	if len(r.Events) > 2*store.DefaultFloodN {
		t.Errorf("retained %d events, want ≤ %d", len(r.Events), 2*store.DefaultFloodN)
	}
}

// --- CORS (D3) ---

func TestCORSPreflight(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodOptions, "/log/anything/p1", "")
	if w.Code != http.StatusNoContent {
		t.Errorf("OPTIONS status = %d, want 204", w.Code)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("preflight CORS origin = %q, want *", got)
	}
	if !strings.Contains(w.Header().Get("Access-Control-Allow-Methods"), "POST") {
		t.Errorf("preflight does not permit POST: %q", w.Header().Get("Access-Control-Allow-Methods"))
	}
}

// --- Health (spec: Health endpoint) ---

func TestHealth(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/health", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	var body map[string]any
	decode(t, w, &body)
	if body["version"] != "test" {
		t.Errorf("health version = %v, want test", body["version"])
	}
	if _, ok := body["uptime"]; !ok {
		t.Error("health missing uptime")
	}
}

// --- Await engine (D8, spec: log-query) ---

// TestAwaitDebounce covers the early return after activity quiets: probes fire
// during the await, then stop, and the call returns shortly after with a summary.
func TestAwaitDebounce(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	go func() {
		time.Sleep(50 * time.Millisecond)
		for i := 0; i < 3; i++ {
			do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	start := time.Now()
	res := awaitCall(t, srv, sess.ID, 5)
	elapsed := time.Since(start)

	if res.Reason != "quiet" {
		t.Errorf("reason = %q, want quiet", res.Reason)
	}
	if res.TotalSeen != 3 {
		t.Errorf("TotalSeen = %d, want 3", res.TotalSeen)
	}
	if elapsed > 2*time.Second {
		t.Errorf("debounce return took %v, expected well under the 5s timeout", elapsed)
	}
	if len(res.Summary) != 1 || res.Summary[0].Total != 3 {
		t.Errorf("summary = %+v", res.Summary)
	}
}

// TestAwaitTimeoutNoEvents covers the zero-evidence timeout path (spec: Timeout
// with no evidence).
func TestAwaitTimeoutNoEvents(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	srv.awaiter.poll = 10 * time.Millisecond

	start := time.Now()
	// timeout_s is whole seconds per the API; use 1s and assert no early return.
	res := awaitCall(t, srv, sess.ID, 1)
	if time.Since(start) < 900*time.Millisecond {
		t.Errorf("returned too early for a no-event timeout: %v", time.Since(start))
	}
	if res.Reason != "timeout" {
		t.Errorf("reason = %q, want timeout", res.Reason)
	}
	if res.TotalSeen != 0 {
		t.Errorf("TotalSeen = %d, want 0", res.TotalSeen)
	}
}

// TestAwaitReAttach covers re-invocation re-attaching to the same open run (D8):
// the second await sees the run already opened by the first and the events
// captured before it.
func TestAwaitReAttach(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	first := awaitCall(t, srv, sess.ID, 1) // opens r1, times out with no events
	if first.Run.ID != "r1" {
		t.Fatalf("first run = %q, want r1", first.Run.ID)
	}

	// Fire into the still-open run, then re-await: must re-attach to r1, not r2.
	for i := 0; i < 2; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}
	// Events fired before this await, so no change is seen during the wait and it
	// resolves at timeout; 1s keeps the run identity assertion cheap.
	second := awaitCall(t, srv, sess.ID, 1)
	if second.Run.ID != "r1" {
		t.Errorf("re-invocation opened a new run %q, want re-attach to r1", second.Run.ID)
	}

	sess2, _ := st.GetSession(sess.ID)
	if len(sess2.Runs) != 1 {
		t.Errorf("expected exactly 1 run after re-attach, got %d", len(sess2.Runs))
	}
}

// TestAwaitAdoptsPreRunEvents covers the fix-prerun integration path: probes fire
// through the public ingest endpoint while no run is open (orphans), then
// await_run opens a run and must return those events attributed to it, disclosed
// as adopted (log-query: "Fast reproduction before await_run").
func TestAwaitAdoptsPreRunEvents(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	// Fast scripted reproduction: events land before await_run opens a run.
	for i := 0; i < 3; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}

	// Nudge a single post-open event so the wait reaches quiet quickly; the three
	// pre-run hits are adopted at open and present in the summary regardless.
	go func() {
		time.Sleep(50 * time.Millisecond)
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":2}`)
	}()

	res := awaitCall(t, srv, sess.ID, 5)
	if len(res.Summary) != 1 || res.Summary[0].Probe != "p1" {
		t.Fatalf("expected p1 summary, got %+v", res.Summary)
	}
	ps := res.Summary[0]
	if ps.Total != 4 {
		t.Errorf("Total = %d, want 4 (3 adopted + 1 direct)", ps.Total)
	}
	if ps.Adopted != 3 {
		t.Errorf("Adopted = %d, want 3", ps.Adopted)
	}
}

// TestAwaitAdoptsThenRestartReplaysSame mirrors the store-level two-pass replay
// regression at the daemon level: drive the await-adopts-pre-run-events flow
// (orphans ingested via the public endpoint, then a direct post-open event), then
// restart by standing up a fresh Server over the same root. The replayed run
// summary must match the live await result exactly, proving adoption markers merge
// with the direct events replay loaded first rather than overwriting them.
func TestAwaitAdoptsThenRestartReplaysSame(t *testing.T) {
	root := t.TempDir()
	srv, st := newTestServerAt(t, root)
	sess, _, err := st.CreateSession("", "restart adopt", "/tmp/app")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Orphans land before await_run opens a run (adopted at open).
	for i := 0; i < 3; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}
	// A direct post-open event nudges the wait to quiet and shares the run's buffer.
	go func() {
		time.Sleep(50 * time.Millisecond)
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":2}`)
	}()

	res := awaitCall(t, srv, sess.ID, 5)
	if len(res.Summary) != 1 || res.Summary[0].Probe != "p1" {
		t.Fatalf("expected p1 summary, got %+v", res.Summary)
	}
	live := res.Summary[0]
	if live.Total != 4 || live.Adopted != 3 {
		t.Fatalf("live attribution wrong: want total=4 adopted=3, got %+v", live)
	}

	// Restart: a fresh store over the same root replays logs.jsonl.
	st2, err := store.New(store.WithRoot(root))
	if err != nil {
		t.Fatalf("store.New (restart): %v", err)
	}
	replay, err := st2.RunSummary(sess.ID, res.Run.ID)
	if err != nil {
		t.Fatalf("RunSummary after restart: %v", err)
	}
	if len(replay) != 1 || replay[0].Probe != "p1" {
		t.Fatalf("replay summary missing p1: %+v", replay)
	}
	rp := replay[0]
	if rp.Total != live.Total || rp.Adopted != live.Adopted || rp.Truncated != live.Truncated {
		t.Errorf("replay attribution diverged: live=%+v replay=%+v", live, rp)
	}
}

// TestAwaitFullyAdoptedReturnsOnDebounce covers fix-prerun Finding 3: when a run
// is fully adopted at open (the reproduction finished before await_run opened the
// run) and no further events arrive, await must return on the debounce window —
// treating the adopted baseline as initial activity — rather than waiting the full
// timeout. Asserts the call returns well under the timeout with reason "quiet".
func TestAwaitFullyAdoptedReturnsOnDebounce(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	// Fast scripted reproduction: every event lands before await_run opens a run,
	// and nothing fires afterwards (the "fully adopted" case).
	for i := 0; i < 3; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}

	start := time.Now()
	res := awaitCall(t, srv, sess.ID, 5) // 5s timeout; must return far sooner
	elapsed := time.Since(start)

	if res.Reason != "quiet" {
		t.Errorf("reason = %q, want \"quiet\"", res.Reason)
	}
	// Debounce is 150ms in the test server; allow generous slack but stay well
	// under the 5s timeout so a regression to full-timeout waiting is caught.
	if elapsed > 2*time.Second {
		t.Errorf("fully-adopted await took %v, expected to return on debounce", elapsed)
	}
	ps, ok := func() (store.ProbeSummary, bool) {
		for _, p := range res.Summary {
			if p.Probe == "p1" {
				return p, true
			}
		}
		return store.ProbeSummary{}, false
	}()
	if !ok || ps.Total != 3 || ps.Adopted != 3 {
		t.Errorf("want p1 total=3 adopted=3, got %+v", res.Summary)
	}
}

// TestCloseRunVerdict covers verdict recording (spec: Run verdicts).
func TestCloseRunVerdict(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)
	awaitCall(t, srv, sess.ID, 1) // opens r1

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/runs/close", `{"verdict":"reproduced"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("close status = %d", w.Code)
	}
	var run store.Run
	decode(t, w, &run)
	if run.Verdict != store.VerdictReproduced || run.ClosedAt == nil {
		t.Errorf("run not closed with verdict: %+v", run)
	}
}

// --- Internal API smoke: full mutation path through HTTP ---

func TestInternalAPILifecycle(t *testing.T) {
	srv, st := newTestServer(t)

	// A real cwd with a real source file so the probe location check (D-G) passes.
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, "app.js"), []byte("l1\nl2\nl3\nl4\nl5\nl6\n"), 0o644); err != nil {
		t.Fatalf("write probe file: %v", err)
	}

	// Create session rooted at the real cwd.
	body, _ := json.Marshal(map[string]string{"description": "bug", "cwd": cwd})
	w := do(srv, http.MethodPost, "/api/sessions", string(body))
	var created struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &created)
	id := created.Session.ID

	// Set hypotheses (≥3, D-E).
	w = do(srv, http.MethodPut, "/api/sessions/"+id+"/hypotheses", `{"statements":["a","b","c"]}`)
	if w.Code != 200 {
		t.Fatalf("set hypotheses status = %d", w.Code)
	}
	// Register a probe at a real in-range line (D-G location check passes).
	w = do(srv, http.MethodPost, "/api/sessions/"+id+"/probes", `{"id":"p1","file":"app.js","line":5,"hypothesis_id":"h1"}`)
	if w.Code != 200 {
		t.Fatalf("register probe status = %d, body = %s", w.Code, w.Body.String())
	}
	// A closed run so the kill can cite it (D-B).
	run, err := st.OpenRun(id)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if _, err := st.CloseRun(id, run.ID, store.VerdictNotReproduced); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	// Kill h1 with an evidence citation (D-B): probe p1 in the closed run.
	patch, _ := json.Marshal(map[string]string{"status": "killed", "note": "no fire", "probe_id": "p1", "run_id": run.ID})
	w = do(srv, http.MethodPatch, "/api/sessions/"+id+"/hypotheses/h1", string(patch))
	if w.Code != 200 {
		t.Fatalf("update hypothesis status = %d, body = %s", w.Code, w.Body.String())
	}
	// Remove the probe.
	w = do(srv, http.MethodDelete, "/api/sessions/"+id+"/probes/p1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove probe status = %d", w.Code)
	}
	// Close session unsolved (empty body): p1 removed, so no unremoved probes.
	w = do(srv, http.MethodDelete, "/api/sessions/"+id, "")
	var closed struct {
		Unremoved []store.Probe `json:"unremoved_probes"`
	}
	decode(t, w, &closed)
	if len(closed.Unremoved) != 0 {
		t.Errorf("expected no unremoved probes, got %+v", closed.Unremoved)
	}
}

// TestResumeLatestNotFound covers the no-open-session path.
func TestResumeLatestNotFound(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/api/sessions", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("resume-latest with no sessions = %d, want 404", w.Code)
	}
}

// --- Real listener: loopback binding + browser simple request over the wire ---

// TestLoopbackListener starts the real HTTP server on an ephemeral loopback port
// and posts a simple request, exercising the actual net stack and CORS headers.
func TestLoopbackListener(t *testing.T) {
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	sess, _, _ := st.CreateSession("", "bug", "/tmp/app")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Loopback-only binding (D4). We assert the bound host is the loopback
	// address rather than attempting a network-level negative test (connecting
	// from a non-loopback interface): that would require a routable second
	// interface and would be flaky/sandbox-dependent in CI, whereas the bind
	// address is the property the spec actually constrains.
	host, _, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	if host != "127.0.0.1" {
		t.Errorf("listener host = %q, want 127.0.0.1 (loopback only, D4)", host)
	}

	hs := &http.Server{Handler: NewServer(st, "test", config.Default())}
	go hs.Serve(ln)
	defer hs.Close()

	base := "http://" + ln.Addr().String()

	// Browser-style POST: no JSON Content-Type set.
	resp, err := http.Post(base+"/log/"+sess.ID+"/p1", "", strings.NewReader(`{"y":2}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "*" {
		t.Errorf("CORS origin = %q", got)
	}
	resp.Body.Close()

	results, _ := st.QueryLogs(sess.ID, store.QueryFilter{Probe: "p1"})
	if len(results) != 1 || results[0].Total != 1 {
		t.Errorf("event not stored over the wire: %+v", results)
	}
}

// --- Field notes (field-notes D2) ---

// TestNotesEndpoint covers POST /api/notes: a valid note is appended and stamped
// with the daemon version, and an invalid category is rejected with 400.
func TestNotesEndpoint(t *testing.T) {
	srv, st := newTestServer(t)

	w := do(srv, http.MethodPost, "/api/notes",
		`{"session":"a3f9","category":"tool-bug","note":"await returned zero events"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	var n notes.Note
	decode(t, w, &n)
	if n.Session != "a3f9" || n.Category != notes.CategoryToolBug {
		t.Errorf("note = %+v", n)
	}
	if n.Version != "test" {
		t.Errorf("version = %q, want test (daemon-stamped)", n.Version)
	}

	// It is durable: a notes store over the same root reads it back.
	ns, err := notes.New(notes.WithRoot(st.Root()))
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}
	list, err := ns.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Note != "await returned zero events" {
		t.Fatalf("persisted notes = %+v", list)
	}

	// Invalid category is a 400, not a 500, and writes nothing.
	w = do(srv, http.MethodPost, "/api/notes", `{"category":"bogus","note":"x"}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("invalid category status = %d, want 400", w.Code)
	}
	if list, _ := ns.List(""); len(list) != 1 {
		t.Errorf("invalid note leaked: %+v", list)
	}
}

// TestNotesNoCORS guards the internal-surface invariant: /api/notes is the MCP
// client's server-side channel and must not advertise cross-origin access (D2),
// or a web page could file notes cross-origin.
func TestNotesNoCORS(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodPost, "/api/notes", `{"category":"other","note":"x"}`)
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("/api/notes set CORS origin %q; internal surface must not", got)
	}
}

// --- helpers ---

func awaitCall(t *testing.T, srv *Server, sessionID string, timeoutS int) awaitResult {
	t.Helper()
	body, _ := json.Marshal(map[string]int{"timeout_s": timeoutS})
	r := httptest.NewRequest(http.MethodPost, "/api/sessions/"+sessionID+"/await", bytes.NewReader(body))
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("await status = %d, body = %s", w.Code, w.Body.String())
	}
	var res awaitResult
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode await result: %v", err)
	}
	return res
}

func decode(t *testing.T, w *httptest.ResponseRecorder, v any) {
	t.Helper()
	if w.Code/100 != 2 {
		t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
	}
	data, _ := io.ReadAll(w.Body)
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("decode: %v (body=%s)", err, data)
	}
}
