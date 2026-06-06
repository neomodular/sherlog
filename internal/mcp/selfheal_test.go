package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/store"
)

// startVersionedDaemon serves a real daemon.Server stamped with the given
// version on addr ("127.0.0.1:0" for any free port), wired the way daemon.Run
// wires it: ShutdownRequested drains the server so POST /api/shutdown actually
// releases the port. Returns the bound port.
func startVersionedDaemon(t *testing.T, addr, version string) (port string) {
	t.Helper()
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}
	_, port, err = net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	handler := daemon.NewServer(st, version, config.Default())
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-handler.ShutdownRequested()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return port
}

// selfHealClient builds a daemonClient of the given version pointed at port.
func selfHealClient(port, version string) *daemonClient {
	return &daemonClient{
		base:      "http://" + net.JoinHostPort("127.0.0.1", port),
		port:      port,
		http:      &http.Client{Timeout: 5 * time.Second},
		awaitHTTP: &http.Client{},
		version:   version,
	}
}

// TestSelfHealOnVersionMismatch covers the core handshake (mcp-server spec:
// Upgrade self-heals on next tool call): a healthy daemon of another version is
// shut down and replaced, and the replacement answers /health with the client's
// own version before ensureDaemon returns.
func TestSelfHealOnVersionMismatch(t *testing.T) {
	port := startVersionedDaemon(t, "127.0.0.1:0", "0.4.0")

	c := selfHealClient(port, "0.5.0")
	spawned := false
	c.spawn = func() error {
		spawned = true
		// The "new binary": a daemon of the client's version on the same port.
		startVersionedDaemon(t, net.JoinHostPort("127.0.0.1", port), "0.5.0")
		return nil
	}

	if err := c.ensureDaemon(context.Background()); err != nil {
		t.Fatalf("ensureDaemon: %v", err)
	}
	if !spawned {
		t.Fatal("mismatch did not trigger a respawn")
	}

	resp, err := http.Get(c.base + "/health")
	if err != nil {
		t.Fatalf("GET /health after self-heal: %v", err)
	}
	defer resp.Body.Close()
	var info struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode /health: %v", err)
	}
	if info.Version != "0.5.0" {
		t.Fatalf("post-heal daemon version = %q, want 0.5.0", info.Version)
	}
}

// TestSameVersionIsNoOp (mcp-server spec: Same version is a no-op) — including
// the dev/dev pairing of local builds. The spawn seam fails the test if touched.
func TestSameVersionIsNoOp(t *testing.T) {
	for _, version := range []string{"0.5.0", "dev"} {
		stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/health" {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"version":"` + version + `","uptime":"1s"}`))
				return
			}
			t.Errorf("version %s: unexpected request %s %s — same version must be a pure no-op", version, r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}))
		_, port, _ := strings.Cut(strings.TrimPrefix(stub.URL, "http://"), ":")
		c := selfHealClient(port, version)
		c.base = stub.URL
		c.spawn = func() error {
			t.Errorf("version %s: spawn called for a same-version daemon", version)
			return nil
		}
		if err := c.ensureDaemon(context.Background()); err != nil {
			t.Errorf("version %s: ensureDaemon = %v, want nil", version, err)
		}
		stub.Close()
	}
}

// TestLegacyDaemonActionableError (mcp-server spec: Pre-endpoint daemon yields
// instructions, not silence): a daemon that 404s /api/shutdown produces an error
// naming both versions and the platform's exact manual kill command.
func TestLegacyDaemonActionableError(t *testing.T) {
	legacy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":"0.4.0","uptime":"1s"}`))
			return
		}
		w.WriteHeader(http.StatusNotFound) // pre-endpoint daemon: no /api/shutdown route
	}))
	defer legacy.Close()

	_, port, _ := strings.Cut(strings.TrimPrefix(legacy.URL, "http://"), ":")
	c := selfHealClient(port, "0.5.0")
	c.base = legacy.URL
	c.spawn = func() error {
		t.Error("spawn must not run when the legacy fallback fires")
		return nil
	}

	err := c.ensureDaemon(context.Background())
	if err == nil {
		t.Fatal("ensureDaemon against a legacy daemon: want actionable error, got nil")
	}
	msg := err.Error()
	for _, want := range []string{"0.4.0", "0.5.0"} {
		if !strings.Contains(msg, want) {
			t.Errorf("legacy error %q does not name version %q", msg, want)
		}
	}
	kill := `pkill -f "sherlog daemon"`
	if runtime.GOOS == "windows" {
		kill = "Stop-Process"
	}
	if !strings.Contains(msg, kill) {
		t.Errorf("legacy error %q lacks the manual kill command %q", msg, kill)
	}
}

// TestConcurrentRestartLoserConverges (mcp-server spec: Concurrent restarts
// converge): the loser's shutdown POST hits an already-exiting daemon (refused
// connection); it must proceed through the handshake and end healthy against
// the new daemon rather than surfacing the race.
func TestConcurrentRestartLoserConverges(t *testing.T) {
	// A freed port stands in for "the old daemon just exited under us".
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close()

	c := selfHealClient(port, "0.5.0")
	c.spawn = func() error {
		startVersionedDaemon(t, net.JoinHostPort("127.0.0.1", port), "0.5.0")
		return nil
	}

	// Drive the mismatch path directly: the winner already tore the old daemon
	// down, so this client's shutdown POST sees a refused connection.
	if err := c.replaceStaleDaemon(context.Background(), "0.4.0"); err != nil {
		t.Fatalf("replaceStaleDaemon after losing the race: %v", err)
	}

	resp, err := http.Get(c.base + "/health")
	if err != nil {
		t.Fatalf("GET /health after convergence: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/health = %d after convergence, want 200", resp.StatusCode)
	}
}
