// Package daemon runs the resident localhost HTTP server: log ingest, session
// state, and the internal API the MCP process calls (D2). The full server is
// implemented in task group 3; this is the entry point the CLI dispatches to.
package daemon

import "fmt"

// Run starts the daemon and blocks until it exits. The HTTP listener, await
// engine, and persistence are wired in task group 3.
func Run() error {
	return fmt.Errorf("daemon: not implemented yet (task group 3)")
}
