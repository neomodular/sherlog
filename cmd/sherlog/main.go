// Command sherlog is a dual-mode binary (D2): `sherlog daemon` runs the resident
// localhost HTTP server, `sherlog mcp` runs the MCP stdio server the plugin
// launches, and `sherlog probes` is the leftover-probe safety net (D10). Dispatch
// is hand-rolled over the stdlib flag package — no CLI framework (project rule).
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"text/tabwriter"
	"time"

	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/mcp"
	"github.com/neomodular/sherlog/internal/notes"
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
	case "notes":
		return cmdNotes(args[1:])
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

// cmdNotes is the `sherlog notes` subcommand: the maintainer's inbox for agent
// field notes about sherlog itself (field-notes spec: Maintainer CLI). It prints
// notes chronologically (oldest to newest) with --category to filter, reading the
// JSONL directly so it works with no daemon running. An absent notes file yields
// empty output, never an error.
func cmdNotes(args []string) error {
	fs := flag.NewFlagSet("notes", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	category := fs.String("category", "", "filter to one category (tool-bug, friction, anomaly, other)")
	if err := fs.Parse(args); err != nil {
		return err
	}

	ns, err := notes.New()
	if err != nil {
		return fmt.Errorf("notes: open store: %w", err)
	}
	return renderNotes(os.Stdout, ns, notes.Category(*category))
}

// renderNotes prints the inbox chronologically (oldest first) to w, optionally
// filtered by category. Split out from cmdNotes so the rendering is testable
// without touching the home directory (field-notes Maintainer CLI).
func renderNotes(w io.Writer, ns *notes.Store, category notes.Category) error {
	list, err := ns.List(category)
	if err != nil {
		return fmt.Errorf("notes: read: %w", err)
	}
	if len(list) == 0 {
		fmt.Fprintln(w, "no field notes")
		return nil
	}

	// Tab-aligned columns so the inbox is easy to skim (field-notes D4). Notes can
	// contain newlines; print the note last so column alignment survives.
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "TIMESTAMP\tCATEGORY\tSESSION\tNOTE")
	for _, n := range list {
		session := n.Session
		if session == "" {
			session = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", n.TS.Format(time.RFC3339), n.Category, session, n.Note)
	}
	return tw.Flush()
}

func usage(w *os.File) {
	fmt.Fprint(w, `sherlog — hypothesis-driven debugging for Claude Code

usage:
  sherlog daemon            run the resident localhost log/state daemon
  sherlog mcp               run the MCP stdio server (launched by the plugin)
  sherlog probes --stale    list probes registered but not yet removed
  sherlog notes [--category <c>]
                            read agent field notes about sherlog itself
  sherlog --version         print the version
`)
}
