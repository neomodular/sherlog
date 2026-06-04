// Package mcp runs the MCP stdio server mode of the binary (D2). It launches the
// official Go SDK server over stdio; the full tool surface (D9) is registered in
// task group 4. Auto-spawning the daemon (D2) is also wired there.
package mcp

import (
	"context"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Run starts the MCP stdio server and blocks until the client disconnects or
// ctx is cancelled. Tools are added in task group 4; the server currently
// completes the handshake and exposes an empty tool set.
func Run(ctx context.Context, version string) error {
	server := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "sherlog",
		Version: version,
	}, nil)

	if err := server.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("mcp: server run: %w", err)
	}
	return nil
}
