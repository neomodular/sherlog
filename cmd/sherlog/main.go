// Command sherlog is a dual-mode binary (D2): `sherlog daemon` runs the resident
// localhost HTTP server, `sherlog mcp` runs the MCP stdio server the plugin
// launches, and `sherlog probes` is the leftover-probe safety net (D10). Dispatch
// is hand-rolled over the stdlib flag package — no CLI framework (project rule).
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"text/tabwriter"

	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/mcp"
	"github.com/neomodular/sherlog/internal/store"
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

// cmdProbes is the `sherlog probes` subcommand. With --stale it lists every
// probe registered but not yet marked removed across all sessions — the
// "weeks later" orphaned-probe safety net (D10). It reads the persisted store
// directly rather than the daemon so it works even when no daemon is running.
func cmdProbes(args []string) error {
	fs := flag.NewFlagSet("probes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	stale := fs.Bool("stale", false, "list probes registered but not yet removed")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if !*stale {
		return fmt.Errorf("probes: specify --stale (the only supported mode)")
	}

	st, err := store.New()
	if err != nil {
		return fmt.Errorf("probes: open store: %w", err)
	}

	staleProbes := st.StaleProbes()
	if len(staleProbes) == 0 {
		fmt.Println("no stale probes — every registered probe has been removed")
		return nil
	}

	// Tab-aligned columns so leftover probes are easy to scan and act on (D10).
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "SESSION\tPROBE\tFILE\tLINE")
	for _, sp := range staleProbes {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\n", sp.SessionID, sp.Probe.ID, sp.Probe.File, sp.Probe.Line)
	}
	return tw.Flush()
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
