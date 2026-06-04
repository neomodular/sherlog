package daemon

import (
	"net/http"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// TestStatsShape covers the stats document shape and that GET /api/stats reflects a
// live session, open run, ingested events, and the trailing-hour count (log-ingest
// spec: Stats reflect activity).
func TestStatsShape(t *testing.T) {
	srv, st := newTestServer(t)
	sess := mustSession(t, st)

	// Open a run, then fire 14 events into it — the spec scenario's numbers.
	awaitCall(t, srv, sess.ID, 1) // opens r1
	const fired = 14
	for i := 0; i < fired; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}

	w := do(srv, http.MethodGet, "/api/stats", "")
	var s Stats
	decode(t, w, &s)

	if s.Vitals.Version != "test" {
		t.Errorf("vitals.version = %q, want test", s.Vitals.Version)
	}
	if s.Vitals.Port != config.DefaultPort {
		t.Errorf("vitals.port = %q, want %q", s.Vitals.Port, config.DefaultPort)
	}
	if s.Vitals.PID != os.Getpid() {
		t.Errorf("vitals.pid = %d, want %d", s.Vitals.PID, os.Getpid())
	}
	if s.Storage.TotalEvents != fired {
		t.Errorf("storage.total_events = %d, want %d", s.Storage.TotalEvents, fired)
	}
	if s.Storage.OpenSessions != 1 {
		t.Errorf("storage.open_sessions = %d, want 1", s.Storage.OpenSessions)
	}
	if s.Activity.HourlyEvents != fired {
		t.Errorf("activity.hourly_events = %d, want %d", s.Activity.HourlyEvents, fired)
	}
	if s.Activity.LastEvent == nil {
		t.Error("activity.last_event is nil, want a timestamp after ingest")
	}
	if s.Activity.OpenRun == nil || s.Activity.OpenRun.Session != sess.ID || s.Activity.OpenRun.Run != "r1" {
		t.Errorf("activity.open_run = %+v, want session %q run r1", s.Activity.OpenRun, sess.ID)
	}
	if s.Config.Sources == nil {
		t.Error("config.sources missing — health view needs per-key sources")
	}
	if _, ok := s.SelfChecks["storage_writable"]; !ok {
		t.Error("self_checks missing storage_writable")
	}
	if c, ok := s.SelfChecks["loopback_only"]; !ok || !c.OK {
		t.Errorf("loopback_only check = %+v, want ok (default bind host)", c)
	}
}

// TestStatsGETOnly guards the read-only surface: non-GET verbs are rejected.
func TestStatsGETOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodDelete} {
		if w := do(srv, m, "/api/stats", ""); w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s /api/stats = %d, want 405", m, w.Code)
		}
	}
}

// TestStatsNoCORS guards the browser-read surface invariant: /api/stats carries no
// CORS, matching the other Case Board read endpoints (case-board-ui D2).
func TestStatsNoCORS(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/api/stats", "")
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("/api/stats set CORS origin %q; browser-read surface must not", got)
	}
}

// TestHealthContractPreserved covers the /health byte-compatibility guarantee
// (log-ingest spec: /health contract preserved): the response is exactly the three
// keys it always had, with no stats fields leaking in.
func TestHealthContractPreserved(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/health", "")
	var body map[string]any
	decode(t, w, &body)

	want := map[string]bool{"version": true, "uptime": true, "config": true}
	if len(body) != len(want) {
		t.Fatalf("/health keys = %v, want exactly %v", keysOf(body), keysOf(map[string]any{"version": 0, "uptime": 0, "config": 0}))
	}
	for k := range body {
		if !want[k] {
			t.Errorf("/health gained unexpected key %q — contract must stay frozen", k)
		}
	}
}

// TestStatsSelfCheckFailureReported covers the self-check failure path (log-ingest
// spec: Self-check failure is reported, not hidden): a non-loopback bind host makes
// loopback_only report ok:false with a detail, while the endpoint still returns 200.
func TestStatsSelfCheckFailureReported(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.SetBindHost("0.0.0.0") // simulate a non-loopback bind

	w := do(srv, http.MethodGet, "/api/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 even with a failing self-check", w.Code)
	}
	var s Stats
	decode(t, w, &s)
	c := s.SelfChecks["loopback_only"]
	if c.OK {
		t.Error("loopback_only ok = true, want false for a 0.0.0.0 bind")
	}
	if c.Detail == "" {
		t.Error("failing self-check has no detail; the view needs the reason text")
	}
}

// TestStorageWritableFailure covers the storage_writable self-check failing when the
// data directory cannot be written, again with a 200 endpoint. Skipped on Windows,
// where a 0o500 directory does not reliably deny writes to the owner.
func TestStorageWritableFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("directory mode bits do not gate owner writes on Windows")
	}
	root := t.TempDir()
	st, err := store.New(store.WithRoot(root))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test", config.Default())

	// Make the root read+execute only so CreateTemp fails.
	if err := os.Chmod(root, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(root, 0o755) // restore so t.TempDir cleanup can remove it

	w := do(srv, http.MethodGet, "/api/stats", "")
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var s Stats
	decode(t, w, &s)
	if s.SelfChecks["storage_writable"].OK {
		t.Error("storage_writable ok = true, want false for a read-only data dir")
	}
}

// TestSubscriberGauge covers the live SSE subscriber gauge (add-health-page D2): a
// connected stream raises the count and disconnecting lowers it back.
func TestSubscriberGauge(t *testing.T) {
	srv, _ := newTestServer(t)
	if got := srv.buildStats(time.Now()).Activity.Subscribers; got != 0 {
		t.Fatalf("initial subscribers = %d, want 0", got)
	}

	// Drive the gauge directly: handleEvents bumps it around its blocking loop, which
	// is awkward to hold open in a recorder, so exercise the atomic the handler uses.
	srv.subscribers.Add(1)
	if got := srv.buildStats(time.Now()).Activity.Subscribers; got != 1 {
		t.Errorf("subscribers after connect = %d, want 1", got)
	}
	srv.subscribers.Add(-1)
	if got := srv.buildStats(time.Now()).Activity.Subscribers; got != 0 {
		t.Errorf("subscribers after disconnect = %d, want 0", got)
	}
}

// TestDiskUsageCache covers the ≤5s disk-usage cache (add-health-page D2): a second
// read within the TTL returns the memoized value rather than re-walking, even after
// the directory grows on disk.
func TestDiskUsageCache(t *testing.T) {
	root := t.TempDir()
	var c diskUsageCache

	if err := os.WriteFile(root+string(os.PathSeparator)+"a", []byte("hello"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	first := c.usage(root)
	if first != 5 {
		t.Fatalf("first usage = %d, want 5", first)
	}

	// Grow the directory; the cached value must stand until the TTL expires.
	if err := os.WriteFile(root+string(os.PathSeparator)+"b", []byte("world!!"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cached := c.usage(root); cached != first {
		t.Errorf("usage within TTL = %d, want cached %d (no re-walk)", cached, first)
	}

	// Force expiry and confirm a re-walk now sees both files.
	c.computed = time.Now().Add(-2 * diskCacheTTL)
	if fresh := c.usage(root); fresh != 12 {
		t.Errorf("usage after TTL = %d, want 12 (re-walked)", fresh)
	}
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
