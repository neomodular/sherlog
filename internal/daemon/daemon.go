// Package daemon runs the resident localhost HTTP server: log ingest, session
// state, and the internal API the MCP process calls (D2). The server binds the
// loopback interface only (D4) and is the source of truth for investigation
// state while it runs (D5, D6).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// DefaultPort is Baker Street 221B (D4): the fixed brand port that makes probe
// lines instantly recognizable in diffs. It mirrors config.DefaultPort so other
// packages (the MCP client) can reference the brand port without importing config.
const DefaultPort = config.DefaultPort

// shutdownGrace bounds the graceful HTTP shutdown after a drained exit
// (restart-on-upgrade D-D). The drain already waited for awaits to finish (gauge
// zero) except in the bounded-fallback case; this only caps how long the normal
// shutdown path waits for any lingering handler before the process exits and the
// next tool call respawns the new binary.
const shutdownGrace = 5 * time.Second

// Run starts the daemon and blocks until the HTTP server exits. Configuration is
// resolved once (env > file > default, add-config) and drives the port, the store
// flood window, the await tuning, and retention pruning. The listener is bound to
// 127.0.0.1 only (D4); SHERLOG_PORT overrides the port. A bind failure — most
// importantly a foreign process already holding the port — is reported with a
// clear, actionable message rather than a raw syscall error.
func Run(version string) error {
	root, err := config.DefaultRoot()
	if err != nil {
		return fmt.Errorf("daemon: resolve config root: %w", err)
	}
	cfg, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("daemon: load config: %w", err)
	}

	st, err := store.New(store.WithFloodN(cfg.FloodKeep))
	if err != nil {
		return fmt.Errorf("daemon: init store: %w", err)
	}

	// Retention pruning runs at startup and on a daily ticker (configuration spec:
	// Retention pruning). retention_days 0 (the default) keeps everything forever.
	startRetention(st, cfg.RetentionDays)

	handler := NewServer(st, version, cfg)

	// Capture the executable's identity BEFORE the listener opens (D-B) so a binary
	// replaced during startup is still caught on the first tick. A failure to resolve
	// or stat the executable disables the watcher for this process — never fatal; the
	// daemon simply keeps the pre-watcher behavior (manual kill), logged once (D-B).
	watcher := newBinaryWatcher(handler)

	addr := net.JoinHostPort("127.0.0.1", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return bindError(addr, cfg.Port, err)
	}

	// Confirm the loopback_only self-check against the real bound host rather than
	// the default (add-health-page D3). JoinHostPort above always uses 127.0.0.1, so
	// this is belt-and-suspenders, but it keeps the check honest if the bind changes.
	if host, _, splitErr := net.SplitHostPort(ln.Addr().String()); splitErr == nil {
		handler.SetBindHost(host)
	}

	if err := serve(handler, ln, watcher); err != nil {
		return fmt.Errorf("daemon: serve on %s: %w", addr, err)
	}
	return nil
}

// newBinaryWatcher builds the self-restart watcher for the daemon's own executable
// (restart-on-upgrade D-B), or returns nil when the executable cannot be resolved
// or stat'd at startup — in which case the watcher is disabled for this process and
// the pre-watcher behavior (a manual kill remains the only way to retire the
// daemon) stays in effect. Either failure is logged once, never fatal. The
// baseline identity is captured here, before the listener opens, so a binary
// replaced mid-startup is caught on the first tick.
func newBinaryWatcher(handler *Server) *binWatcher {
	exe, err := os.Executable()
	if err != nil {
		log.Printf("sherlog: binary watch disabled (cannot resolve executable path): %v", err)
		return nil
	}
	baseline, err := captureBinIdentity(exe)
	if err != nil {
		log.Printf("sherlog: binary watch disabled (cannot stat executable %q): %v", exe, err)
		return nil
	}
	return &binWatcher{
		path:     exe,
		interval: binaryWatchInterval,
		baseline: baseline,
		maxDrain: handler.awaiter.maxTimeout,
		inFlight: func() int64 { return handler.awaiter.inFlight.Load() },
	}
}

// serve runs the HTTP server and, when a watcher is present, the binary watcher
// alongside it (restart-on-upgrade D-D). It returns nil when the watcher drains the
// daemon toward a clean exit (so Run exits 0 and the next tool call respawns the
// binary now on disk), or the server's own error when it stops for any other reason.
// A nil watcher means the watcher was disabled at startup: the daemon just serves
// until the server stops on its own, exactly as before this change.
func serve(handler *Server, ln net.Listener, watcher *binWatcher) error {
	srv := &http.Server{
		Handler: handler,
		// Generous header timeout guards against slowloris on a localhost-only
		// service; no overall write/read timeout because await_run long-polls.
		ReadHeaderTimeout: 5 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	if watcher == nil {
		return <-serveErr
	}

	stopWatch := make(chan struct{})
	drained := make(chan struct{}, 1)
	go func() {
		if watcher.run(stopWatch) {
			drained <- struct{}{}
		}
	}()

	select {
	case err := <-serveErr:
		// The server stopped on its own (a fatal serve error). Tear the watcher down
		// and report the error.
		close(stopWatch)
		return err
	case <-drained:
		// The watcher observed a replaced binary and drained: shut the server down
		// through the graceful path (D-D). Awaits have already returned (gauge zero)
		// in the normal case; the bounded fallback caps how long a wedged await can
		// hold shutdown. Serve returns ErrServerClosed once the listener closes, so
		// the goroutine reports nil below.
		ctx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		_ = srv.Shutdown(ctx)
		return <-serveErr
	}
}

// bindError turns a listen failure into an actionable message. "Address already
// in use" means a foreign process (or a stale daemon) holds the port: the only
// safe automated response is to fail fast and tell the user how to override (D4).
func bindError(addr, port string, err error) error {
	if isAddrInUse(err) {
		return fmt.Errorf(
			"daemon: port %s is already in use by another process — stop it or set SHERLOG_PORT to a free port (probe URLs follow the daemon's port automatically): %w",
			port, err)
	}
	return fmt.Errorf("daemon: cannot listen on %s: %w", addr, err)
}

// isAddrInUse reports whether err is an "address already in use" bind failure.
// The portable EADDRINUSE matches on Unix; Windows surfaces the same condition
// as WSAEADDRINUSE (errno 10048), so we compare the Errno value directly to stay
// cross-platform without build tags.
func isAddrInUse(err error) bool {
	if errors.Is(err, syscall.EADDRINUSE) {
		return true
	}
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return errno == wsaEAddrInUse
	}
	return false
}

// wsaEAddrInUse is the Windows Sockets WSAEADDRINUSE value. On non-Windows it is
// never produced, so the comparison above is harmless.
const wsaEAddrInUse = syscall.Errno(10048)
