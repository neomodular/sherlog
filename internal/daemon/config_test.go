package daemon

import (
	"net/http"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// serverWithConfig builds a test Server over a temp-dir store with an explicit
// effective config so config-wiring tests can assert knob propagation.
func serverWithConfig(t *testing.T, cfg config.Effective) (*Server, *store.Store) {
	t.Helper()
	st, err := store.New(store.WithRoot(t.TempDir()), store.WithFloodN(cfg.FloodKeep))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	srv := NewServer(st, "test", cfg)
	srv.awaiter.poll = 10 * time.Millisecond
	return srv, st
}

// TestHealthExposesEffectiveConfig covers the observability requirement: /health
// includes the effective config with values and sources (configuration spec:
// "Checking why awaits end early").
func TestHealthExposesEffectiveConfig(t *testing.T) {
	cfg := config.Default()
	cfg.AwaitDebounceSeconds = 5
	cfg.Sources[config.KeyAwaitDebounceSeconds] = config.SourceFile

	srv, _ := serverWithConfig(t, cfg)
	w := do(srv, http.MethodGet, "/health", "")
	var body struct {
		Config config.Effective `json:"config"`
	}
	decode(t, w, &body)

	if body.Config.AwaitDebounceSeconds != 5 {
		t.Errorf("health config debounce = %d, want 5", body.Config.AwaitDebounceSeconds)
	}
	if body.Config.Sources[config.KeyAwaitDebounceSeconds] != config.SourceFile {
		t.Errorf("health config debounce source = %q, want file", body.Config.Sources[config.KeyAwaitDebounceSeconds])
	}
}

// TestBiggerFloodWindow covers flood_keep wiring (configuration spec: "Bigger
// flood window"): with flood_keep 50 and 1,000 events, first 50 + last 50 are
// retained while the total reads exactly 1,000.
func TestBiggerFloodWindow(t *testing.T) {
	cfg := config.Default()
	cfg.FloodKeep = 50
	srv, st := serverWithConfig(t, cfg)
	sess := mustSession(t, st)

	const fired = 1000
	for i := 0; i < fired; i++ {
		do(srv, http.MethodPost, "/log/"+sess.ID+"/p1", `{"i":1}`)
	}

	results, err := st.QueryLogs(sess.ID, store.QueryFilter{Probe: "p1"})
	if err != nil {
		t.Fatalf("QueryLogs: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("want 1 bucket, got %d", len(results))
	}
	r := results[0]
	if r.Total != fired {
		t.Errorf("Total = %d, want %d", r.Total, fired)
	}
	if len(r.Events) != 2*cfg.FloodKeep {
		t.Errorf("retained %d events, want %d (first 50 + last 50)", len(r.Events), 2*cfg.FloodKeep)
	}
}

// TestCreateSessionDeliversPreferences covers preference delivery (design D4): the
// create-session response carries the effective verbosity and color so debug_start
// can forward them to the skill.
func TestCreateSessionDeliversPreferences(t *testing.T) {
	cfg := config.Default()
	cfg.Verbosity = config.VerbosityMinimal
	cfg.Color = config.ColorNever
	srv, _ := serverWithConfig(t, cfg)

	w := do(srv, http.MethodPost, "/api/sessions", `{"description":"bug","cwd":"/tmp/x"}`)
	var body struct {
		Preferences struct {
			Verbosity string `json:"verbosity"`
			Color     string `json:"color"`
		} `json:"preferences"`
	}
	decode(t, w, &body)
	if body.Preferences.Verbosity != config.VerbosityMinimal || body.Preferences.Color != config.ColorNever {
		t.Errorf("preferences = %+v, want minimal/never", body.Preferences)
	}
}

// TestAwaitDebounceZeroReachesEngine guards the knob-fidelity requirement: a
// debounce of 0 is a valid, spec-allowed value (range 0–30) and must reach the
// engine intact rather than being silently rebased to a default. Otherwise
// `config set await_debounce_seconds 0` and /health would report 0 while the loop
// ran at a different value.
func TestAwaitDebounceZeroReachesEngine(t *testing.T) {
	cfg := config.Default()
	cfg.AwaitDebounceSeconds = 0
	srv, _ := serverWithConfig(t, cfg)
	if srv.awaiter.debounce != 0 {
		t.Errorf("engine debounce = %v, want 0 from config", srv.awaiter.debounce)
	}
}

// TestAwaitMaxTimeoutClamp covers the configured clamp: a client timeout above
// the configured max-timeout is clamped to it. The clamp is asserted directly on
// the engine value (the wait itself returning at the clamp would be slow to test).
func TestAwaitMaxTimeoutClamp(t *testing.T) {
	cfg := config.Default()
	cfg.AwaitMaxTimeoutSeconds = 30
	srv, _ := serverWithConfig(t, cfg)
	if srv.awaiter.maxTimeout != 30*time.Second {
		t.Errorf("engine maxTimeout = %v, want 30s from config", srv.awaiter.maxTimeout)
	}
}
