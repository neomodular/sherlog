// Command sherlog is a dual-mode binary (D2): `sherlog daemon` runs the resident
// localhost HTTP server, `sherlog mcp` runs the MCP stdio server the plugin
// launches, and `sherlog probes` is the leftover-probe safety net (D10). Dispatch
// is hand-rolled over the stdlib flag package — no CLI framework (project rule).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/mcp"
)

// version is overridden at release time via -ldflags (D14: binary and plugin
// version together).
var version = "dev"

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sherlog:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) == 0 {
		usage(os.Stderr)
		return fmt.Errorf("no subcommand given")
	}

	switch cmd := args[0]; cmd {
	case "daemon":
		return daemon.Run(version)
	case "mcp":
		// Cancel the MCP server on interrupt so stdio shuts down cleanly.
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
		defer stop()
		return mcp.Run(ctx, version)
	case "probes":
		return cmdProbes(args[1:])
	case "--version", "-version", "version":
		fmt.Println("sherlog", version)
		return nil
	case "--help", "-h", "help":
		usage(os.Stdout)
		return nil
	default:
		usage(os.Stderr)
		return fmt.Errorf("unknown subcommand %q", cmd)
	}
}

// cmdProbes is the `sherlog probes` subcommand. The --stale listing (D10) lands
// in task group 5; this stub keeps the dispatch surface complete and buildable.
func cmdProbes(_ []string) error {
	return fmt.Errorf("probes: not implemented yet (task group 5)")
}

func usage(w *os.File) {
	fmt.Fprint(w, `sherlog — hypothesis-driven debugging for Claude Code

usage:
  sherlog daemon          run the resident localhost log/state daemon
  sherlog mcp             run the MCP stdio server (launched by the plugin)
  sherlog probes --stale  list probes registered but not yet removed
  sherlog --version       print the version
`)
}
