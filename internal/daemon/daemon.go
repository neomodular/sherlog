// Package daemon runs the resident localhost HTTP server: log ingest, session
// state, and the internal API the MCP process calls (D2). The server binds the
// loopback interface only (D4) and is the source of truth for investigation
// state while it runs (D5, D6).
package daemon

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// DefaultPort is Baker Street 221B (D4): the fixed brand port that makes probe
// lines instantly recognizable in diffs.
const DefaultPort = "2218"

// Run starts the daemon and blocks until the HTTP server exits. The listener is
// bound to 127.0.0.1 only (D4); SHERLOG_PORT overrides the port. A bind failure
// — most importantly a foreign process already holding the port — is reported
// with a clear, actionable message rather than a raw syscall error.
func Run(version string) error {
	st, err := store.New()
	if err != nil {
		return fmt.Errorf("daemon: init store: %w", err)
	}

	port := os.Getenv("SHERLOG_PORT")
	if port == "" {
		port = DefaultPort
	}
	addr := net.JoinHostPort("127.0.0.1", port)

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return bindError(addr, port, err)
	}

	srv := &http.Server{
		Handler: NewServer(st, version),
		// Generous header timeout guards against slowloris on a localhost-only
		// service; no overall write/read timeout because await_run long-polls.
		ReadHeaderTimeout: 5 * time.Second,
	}

	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("daemon: serve on %s: %w", addr, err)
	}
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
