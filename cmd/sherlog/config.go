package main

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/neomodular/sherlog/internal/config"
)

// cmdConfig is the `sherlog config list|get|set` subcommand (configuration spec:
// Config CLI). list prints every key's effective value and source; get prints one
// value; set validates and writes one key atomically. Output goes to w so the
// rendering is testable without touching stdout.
func cmdConfig(w io.Writer, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config: specify list, get <key>, or set <key> <value>")
	}

	root, err := config.DefaultRoot()
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}

	switch sub := args[0]; sub {
	case "list":
		return configList(w, root)
	case "get":
		if len(args) != 2 {
			return fmt.Errorf("config get: requires exactly one key")
		}
		return configGet(w, root, args[1])
	case "set":
		if len(args) != 3 {
			return fmt.Errorf("config set: requires a key and a value")
		}
		return configSet(w, root, args[1], args[2])
	default:
		return fmt.Errorf("config: unknown subcommand %q (want list, get, or set)", sub)
	}
}

// configList prints every key, its effective value, and its source in a
// tab-aligned table — the diagnosability win (design D3).
func configList(w io.Writer, root string) error {
	eff, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("config list: %w", err)
	}
	tw := tabwriter.NewWriter(w, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "KEY\tVALUE\tSOURCE")
	for _, key := range config.Keys {
		val, err := eff.ValueString(key)
		if err != nil {
			return err
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\n", key, val, eff.Sources[key])
	}
	return tw.Flush()
}

// configGet prints the effective value of one key (no source decoration, so it is
// scriptable).
func configGet(w io.Writer, root, key string) error {
	eff, err := config.Load(root)
	if err != nil {
		return fmt.Errorf("config get: %w", err)
	}
	val, err := eff.ValueString(key)
	if err != nil {
		return fmt.Errorf("config get: %w", err)
	}
	fmt.Fprintln(w, val)
	return nil
}

// configSet validates and writes one key, confirming the write action rather than
// asserting an effective source: an env override (e.g. SHERLOG_PORT) can still win
// over the file, so claiming "source: file" here would mislead. `config list`
// remains the place to read effective sources.
func configSet(w io.Writer, root, key, value string) error {
	if err := config.Set(root, key, value); err != nil {
		return fmt.Errorf("config set: %w", err)
	}
	fmt.Fprintf(w, "wrote %s = %s to %s\n", key, value, config.Path(root))
	return nil
}
