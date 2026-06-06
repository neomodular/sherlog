// Package daemon runs the resident localhost HTTP server: log ingest, session
// state, and the internal API the MCP process calls (D2). The server binds the
// loopback interface only (D4) and is the source of truth for investigation
// state while it runs (D5, D6).
package daemon

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// DefaultPort is Baker Street 221B (D4): the fixed brand port that makes probe
// lines instantly recognizable in diffs. It mirrors config.DefaultPort so other
// packages (the MCP client) can reference the brand port without importing config.
const DefaultPort = config.DefaultPort

// shutdownDrainBudget bounds the graceful drain after POST /api/shutdown
// (daemon-self-heal-on-upgrade D3). It must stay short: await_run long-polls can
// hold connections for minutes, and srv.Shutdown waits for them — after the
// budget the server is hard-closed, which is safe because all investigation
// state is persisted at write time (MVP D5/D6).
const shutdownDrainBudget = 2 * time.Second

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

	addr := net.JoinHostPort("127.0.0.1", cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return bindError(addr, cfg.Port, err)
	}

	handler := NewServer(st, version, cfg)
	// Confirm the loopback_only self-check against the real bound host rather than
	// the default (add-health-page D3). JoinHostPort above always uses 127.0.0.1, so
	// this is belt-and-suspenders, but it keeps the check honest if the bind changes.
	if host, _, splitErr := net.SplitHostPort(ln.Addr().String()); splitErr == nil {
		handler.SetBindHost(host)
	}

	srv := &http.Server{
		Handler: handler,
		// Generous header timeout guards against slowloris on a localhost-only
		// service; no overall write/read timeout because await_run long-polls.
		ReadHeaderTimeout: 5 * time.Second,
	}

	// Graceful stop on POST /api/shutdown (daemon-self-heal-on-upgrade D3): drain
	// politely within the budget, then hard-close so a held await_run long-poll
	// cannot stall the exit. Serve returns ErrServerClosed as soon as Shutdown is
	// *called*, so Run must wait for the drain to finish before returning —
	// otherwise the process could exit before the 200 ack flushes to the client.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		<-handler.ShutdownRequested()
		ctx, cancel := context.WithTimeout(context.Background(), shutdownDrainBudget)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}()

	// Serve always returns non-nil: ErrServerClosed via the shutdown path above
	// (wait for the drain, then exit 0), or a real serve failure (report it).
	if err := srv.Serve(ln); !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("daemon: serve on %s: %w", addr, err)
	}
	<-drained
	return nil
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
