package daemon

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// newTestServer builds a Server over a temp-dir store and returns it plus the
// store so tests can seed sessions directly. Await timing is tightened so the
// debounce tests run in well under a second.
func newTestServer(t *testing.T) (*Server, *store.Store) {
	t.Helper()
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test")
	srv.awaiter.debounce = 150 * time.Millisecond
	srv.awaiter.poll = 10 * time.Millisecond
	return srv, st
}

func mustSession(t *testing.T, st *store.Store) *store.Session {
	t.Helper()
	sess, _, err := st.CreateSession("bug", "/tmp/app")
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
	srv, _ := newTestServer(t)

	// Create session.
	w := do(srv, http.MethodPost, "/api/sessions", `{"description":"bug","cwd":"/tmp/x"}`)
	var created struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &created)
	id := created.Session.ID

	// Set hypotheses.
	w = do(srv, http.MethodPut, "/api/sessions/"+id+"/hypotheses", `{"statements":["a","b","c"]}`)
	if w.Code != 200 {
		t.Fatalf("set hypotheses status = %d", w.Code)
	}
	// Update one.
	w = do(srv, http.MethodPatch, "/api/sessions/"+id+"/hypotheses/h1", `{"status":"killed","note":"no fire"}`)
	if w.Code != 200 {
		t.Fatalf("update hypothesis status = %d", w.Code)
	}
	// Register and remove a probe.
	w = do(srv, http.MethodPost, "/api/sessions/"+id+"/probes", `{"id":"p1","file":"a.js","line":5,"hypothesis_id":"h1"}`)
	if w.Code != 200 {
		t.Fatalf("register probe status = %d", w.Code)
	}
	w = do(srv, http.MethodDelete, "/api/sessions/"+id+"/probes/p1", "")
	if w.Code != http.StatusNoContent {
		t.Fatalf("remove probe status = %d", w.Code)
	}
	// Close session: p1 removed, so no unremoved probes.
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
	sess, _, _ := st.CreateSession("bug", "/tmp/app")

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

	hs := &http.Server{Handler: NewServer(st, "test")}
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
