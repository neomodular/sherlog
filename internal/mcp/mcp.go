// Package mcp runs the MCP stdio server mode of the binary (D2). It exposes the
// investigation tool surface (D9) over stdio and talks to the resident daemon
// over localhost HTTP, auto-spawning the daemon if it is not already running.
package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run starts the MCP stdio server and blocks until the client disconnects or ctx
// is cancelled. It registers the full tool set and wires each tool to the daemon
// client; the daemon is health-checked and auto-spawned on startup and again on
// every tool call (D2), so a fresh machine needs no separate daemon setup.
func Run(ctx context.Context, version string) error {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "sherlog",
		Version: version,
	}, nil)

	c := newDaemonClient()
	registerTools(server, c)

	// Best-effort startup warm-up: the spec requires a daemon health check "on
	// startup and on first tool call". Spawn or surface a foreign-port conflict
	// early, but never block server startup on it — the per-tool check (D2) is the
	// authoritative gate and will report any persistent failure to the model.
	_ = c.ensureDaemon(ctx)

	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp: server run: %w", err)
	}
	return nil
}
