package mcp

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestBuildProbeContract asserts the probe one-liners are present, embed the
// session URL, and never set a JSON Content-Type (D3).
func TestBuildProbeContract(t *testing.T) {
	tmpl := "http://127.0.0.1:2218/log/abc123/<probe>"
	pc := buildProbeContract(tmpl)

	for _, lang := range []string{"js", "python", "go", "ruby", "curl"} {
		line, ok := pc.OneLiners[lang]
		if !ok || line == "" {
			t.Errorf("missing one-liner for %q", lang)
			continue
		}
		if !strings.Contains(line, tmpl) {
			t.Errorf("%s one-liner does not contain URL template: %s", lang, line)
		}
		// No probe form may declare a JSON content type — that would trigger a
		// browser preflight (D3).
		if strings.Contains(strings.ToLower(line), "application/json") {
			t.Errorf("%s one-liner sets JSON content type (breaks CORS simple request): %s", lang, line)
		}
	}
}

// TestGreppableFragment trims the <probe> placeholder to the per-session prefix
// that matches every probe line in source (D10).
func TestGreppableFragment(t *testing.T) {
	got := greppableFragment("http://127.0.0.1:2218/log/abc123/<probe>")
	want := "http://127.0.0.1:2218/log/abc123/"
	if got != want {
		t.Fatalf("greppableFragment = %q, want %q", got, want)
	}
}

// TestEnsureDaemonForeignPort: a listener that is up but does not answer /health
// as sherlog must be reported as a foreign-port conflict, never spawned over (D4).
func TestEnsureDaemonForeignPort(t *testing.T) {
	// A foreign server: 404 on everything, so /health never returns sherlog JSON.
	foreign := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer foreign.Close()

	addr := strings.TrimPrefix(foreign.URL, "http://")
	_, port, ok := strings.Cut(addr, ":")
	if !ok {
		t.Fatalf("cannot split %q", addr)
	}

	c := &daemonClient{
		base:      foreign.URL,
		port:      port,
		http:      &http.Client{Timeout: time.Second},
		awaitHTTP: &http.Client{},
	}

	err := c.ensureDaemon(context.Background())
	if err == nil {
		t.Fatal("ensureDaemon: want foreign-port error, got nil")
	}
	if !strings.Contains(err.Error(), "SHERLOG_PORT") {
		t.Errorf("error should mention SHERLOG_PORT override, got: %v", err)
	}
}

// TestEnsureDaemonHealthy: when the daemon answers /health as sherlog, ensureDaemon
// is a no-op (no spawn attempt).
func TestEnsureDaemonHealthy(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"test","uptime":"1s"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer healthy.Close()

	addr := strings.TrimPrefix(healthy.URL, "http://")
	_, port, _ := strings.Cut(addr, ":")
	c := &daemonClient{
		base:      healthy.URL,
		port:      port,
		version:   "test", // matches the daemon's reported version → no mismatch note
		http:      &http.Client{Timeout: time.Second},
		awaitHTTP: &http.Client{},
	}
	if err := c.ensureDaemon(context.Background()); err != nil {
		t.Fatalf("ensureDaemon against healthy daemon: %v", err)
	}
}

// TestEnsureDaemonVersionDisclosure covers the stale-client disclosure (D-E):
// a version delta between the daemon's /health and this process's compiled
// version emits exactly one informational line per process (sync.Once) across many
// tool calls, matching versions stay silent, and neither path changes behavior —
// ensureDaemon still returns nil every time.
func TestEnsureDaemonVersionDisclosure(t *testing.T) {
	tests := []struct {
		name          string
		clientVersion string
		daemonVersion string
		calls         int
		wantLines     int
	}{
		{name: "stale client notes mismatch once", clientVersion: "v0.8.0", daemonVersion: "v0.9.0", calls: 5, wantLines: 1},
		{name: "matching versions stay silent", clientVersion: "v0.9.0", daemonVersion: "v0.9.0", calls: 5, wantLines: 0},
		{name: "dev vs dev is not a mismatch", clientVersion: "dev", daemonVersion: "dev", calls: 3, wantLines: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			daemonVersion := tt.daemonVersion
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/health" {
					w.Header().Set("Content-Type", "application/json")
					fmt.Fprintf(w, `{"version":%q,"uptime":"1s"}`, daemonVersion)
					return
				}
				w.WriteHeader(http.StatusNotFound)
			}))
			defer srv.Close()

			addr := strings.TrimPrefix(srv.URL, "http://")
			_, port, _ := strings.Cut(addr, ":")

			var mu sync.Mutex
			var lines []string
			c := &daemonClient{
				base:      srv.URL,
				port:      port,
				version:   tt.clientVersion,
				http:      &http.Client{Timeout: time.Second},
				awaitHTTP: &http.Client{},
				logf: func(format string, args ...any) {
					mu.Lock()
					defer mu.Unlock()
					lines = append(lines, fmt.Sprintf(format, args...))
				},
			}

			for i := 0; i < tt.calls; i++ {
				if err := c.ensureDaemon(context.Background()); err != nil {
					t.Fatalf("ensureDaemon call %d: %v", i, err) // no behavior change: always nil
				}
			}

			mu.Lock()
			defer mu.Unlock()
			if len(lines) != tt.wantLines {
				t.Fatalf("emitted %d disclosure line(s), want %d: %v", len(lines), tt.wantLines, lines)
			}
			if tt.wantLines == 1 {
				got := lines[0]
				if !strings.Contains(got, tt.daemonVersion) || !strings.Contains(got, tt.clientVersion) {
					t.Errorf("disclosure line should name both versions, got: %q", got)
				}
				if !strings.Contains(strings.ToLower(got), "restart") {
					t.Errorf("disclosure line should mention a session restart, got: %q", got)
				}
			}
		})
	}
}

// TestAwaitReattachesAcrossCalls: two consecutive await_run calls with no probe
// activity must attach to the same open run (D8: re-invocable for long repros).
func TestAwaitReattachesAcrossCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)

	var start debugStartOut
	callTool(t, ctx, sess, "debug_start", map[string]any{"bug_description": "slow repro"}, &start)

	call := newAwaitCaller(ctx, sess)
	first, err := call("await_run", map[string]any{"session_id": start.SessionID, "timeout_s": 1})
	if err != nil {
		t.Fatalf("first await_run: %v", err)
	}
	second, err := call("await_run", map[string]any{"session_id": start.SessionID, "timeout_s": 1})
	if err != nil {
		t.Fatalf("second await_run: %v", err)
	}
	if first.Run.ID != second.Run.ID {
		t.Fatalf("await_run opened a new run on re-invoke: %s then %s", first.Run.ID, second.Run.ID)
	}
	if first.Reason != "timeout" {
		t.Errorf("first await reason = %q, want timeout (no activity)", first.Reason)
	}
}
