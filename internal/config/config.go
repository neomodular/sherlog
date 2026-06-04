// Package config is sherlog's single configuration source (design D1/D2): a typed
// schema loaded from ~/.sherlog/config.json with strict unknown-key rejection,
// resolved exactly once against environment overrides and built-in defaults into
// an Effective value (treated as immutable after Load) passed to the daemon, store,
// and skill. No other
// package reads config files or environment knobs — resolution lives here so the
// rest of the code takes plain injected values (keeps testability).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Knob key constants — the public names used by the CLI and /health source map so
// keys are spelled in exactly one place (DRY).
const (
	KeyPort                   = "port"
	KeyFloodKeep              = "flood_keep"
	KeyAwaitDebounceSeconds   = "await_debounce_seconds"
	KeyAwaitMaxTimeoutSeconds = "await_max_timeout_seconds"
	KeyRetentionDays          = "retention_days"
	KeyVerbosity              = "verbosity"
	KeyColor                  = "color"
)

// Built-in defaults (design D2): identical to the MVP constants so an absent
// config file reproduces today's behavior exactly. Port mirrors daemon.DefaultPort
// (Baker Street 221B) but is declared here to keep config the single resolution
// point; the daemon consumes the resolved value rather than its own constant.
const (
	DefaultPort                   = "2218"
	DefaultFloodKeep              = 20
	DefaultAwaitDebounceSeconds   = 2
	DefaultAwaitMaxTimeoutSeconds = 600
	DefaultRetentionDays          = 0 // 0 = keep forever
	DefaultVerbosity              = VerbosityDetective
	DefaultColor                  = ColorAuto
)

// Verbosity values control skill presentation (design D4): detective keeps the
// theater, minimal drops it while keeping every loop obligation.
const (
	VerbosityDetective = "detective"
	VerbosityMinimal   = "minimal"
)

// Color values control ANSI handling in the skill (design D4).
const (
	ColorAuto   = "auto"
	ColorAlways = "always"
	ColorNever  = "never"
)

// envPort is the one environment override that exists today and stays
// authoritative (design D2: SHERLOG_PORT keeps working).
const envPort = "SHERLOG_PORT"

// configFileName is the on-disk file under the storage root (design D1).
const configFileName = "config.json"

// Source records where an effective value came from, surfaced by `config list`
// and /health for diagnosability (design D3).
type Source string

const (
	// SourceDefault means the built-in default was used (no file/env override).
	SourceDefault Source = "default"
	// SourceFile means the value came from config.json.
	SourceFile Source = "file"
	// SourceEnv means an environment variable overrode the file/default.
	SourceEnv Source = "env"
)

// file is the strict on-disk schema. Pointer fields distinguish "absent" from
// "set to the zero value", which is what lets precedence and source tracking be
// exact (a file that sets flood_keep:0 is a validation error, not "unset").
type file struct {
	Port                   *string `json:"port,omitempty"`
	FloodKeep              *int    `json:"flood_keep,omitempty"`
	AwaitDebounceSeconds   *int    `json:"await_debounce_seconds,omitempty"`
	AwaitMaxTimeoutSeconds *int    `json:"await_max_timeout_seconds,omitempty"`
	RetentionDays          *int    `json:"retention_days,omitempty"`
	Verbosity              *string `json:"verbosity,omitempty"`
	Color                  *string `json:"color,omitempty"`
}

// Effective is the post-precedence configuration consumed everywhere (design D2).
// It is treated as immutable after Load: callers take it by value and must not
// mutate the shared Sources map. Sources maps each knob key to where its value
// originated.
type Effective struct {
	Port                   string `json:"port"`
	FloodKeep              int    `json:"flood_keep"`
	AwaitDebounceSeconds   int    `json:"await_debounce_seconds"`
	AwaitMaxTimeoutSeconds int    `json:"await_max_timeout_seconds"`
	RetentionDays          int    `json:"retention_days"`
	Verbosity              string `json:"verbosity"`
	Color                  string `json:"color"`

	Sources map[string]Source `json:"sources"`
}

// Default returns the effective configuration with every value at its built-in
// default and every source marked default. It is the single place defaults are
// expressed (design D1).
func Default() Effective {
	return Effective{
		Port:                   DefaultPort,
		FloodKeep:              DefaultFloodKeep,
		AwaitDebounceSeconds:   DefaultAwaitDebounceSeconds,
		AwaitMaxTimeoutSeconds: DefaultAwaitMaxTimeoutSeconds,
		RetentionDays:          DefaultRetentionDays,
		Verbosity:              DefaultVerbosity,
		Color:                  DefaultColor,
		Sources: map[string]Source{
			KeyPort:                   SourceDefault,
			KeyFloodKeep:              SourceDefault,
			KeyAwaitDebounceSeconds:   SourceDefault,
			KeyAwaitMaxTimeoutSeconds: SourceDefault,
			KeyRetentionDays:          SourceDefault,
			KeyVerbosity:              SourceDefault,
			KeyColor:                  SourceDefault,
		},
	}
}

// DefaultRoot resolves the storage root (~/.sherlog), matching the store and
// notes packages so all local sherlog data lives in one directory.
func DefaultRoot() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir for config root: %w", err)
	}
	return filepath.Join(home, ".sherlog"), nil
}

// Path returns the config file path under root.
func Path(root string) string { return filepath.Join(root, configFileName) }

// Load resolves the effective configuration once (design D2): built-in defaults,
// overlaid by config.json under root (strict — unknown keys fail), overlaid by
// environment overrides. An absent file yields defaults (current MVP behavior).
// Validation rules are applied to file values so a malformed file fails loudly at
// startup rather than producing surprising behavior.
func Load(root string) (Effective, error) {
	eff := Default()

	f, err := readFile(Path(root))
	if err != nil {
		return Effective{}, err
	}
	if f != nil {
		if err := applyFile(&eff, f); err != nil {
			return Effective{}, err
		}
	}

	applyEnv(&eff)
	return eff, nil
}

// readFile reads and strictly decodes config.json. A missing file is not an error
// (returns nil); unknown keys and malformed JSON are errors with a clear message.
func readFile(path string) (*file, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read config %s: %w", path, err)
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields() // catch typos like "flod_keep" early (design D1)
	var f file
	if err := dec.Decode(&f); err != nil {
		return nil, fmt.Errorf("parse config %s: %w", path, err)
	}
	return &f, nil
}

// applyFile overlays validated file values onto eff, marking each overridden knob
// as file-sourced. Out-of-range or invalid values fail loading (design D1/D3).
func applyFile(eff *Effective, f *file) error {
	if f.Port != nil {
		if err := validatePort(*f.Port); err != nil {
			return err
		}
		eff.Port = *f.Port
		eff.Sources[KeyPort] = SourceFile
	}
	if f.FloodKeep != nil {
		if err := validateFloodKeep(*f.FloodKeep); err != nil {
			return err
		}
		eff.FloodKeep = *f.FloodKeep
		eff.Sources[KeyFloodKeep] = SourceFile
	}
	if f.AwaitDebounceSeconds != nil {
		if err := validateDebounce(*f.AwaitDebounceSeconds); err != nil {
			return err
		}
		eff.AwaitDebounceSeconds = *f.AwaitDebounceSeconds
		eff.Sources[KeyAwaitDebounceSeconds] = SourceFile
	}
	if f.AwaitMaxTimeoutSeconds != nil {
		if err := validateMaxTimeout(*f.AwaitMaxTimeoutSeconds); err != nil {
			return err
		}
		eff.AwaitMaxTimeoutSeconds = *f.AwaitMaxTimeoutSeconds
		eff.Sources[KeyAwaitMaxTimeoutSeconds] = SourceFile
	}
	if f.RetentionDays != nil {
		if err := validateRetentionDays(*f.RetentionDays); err != nil {
			return err
		}
		eff.RetentionDays = *f.RetentionDays
		eff.Sources[KeyRetentionDays] = SourceFile
	}
	if f.Verbosity != nil {
		if err := validateVerbosity(*f.Verbosity); err != nil {
			return err
		}
		eff.Verbosity = *f.Verbosity
		eff.Sources[KeyVerbosity] = SourceFile
	}
	if f.Color != nil {
		if err := validateColor(*f.Color); err != nil {
			return err
		}
		eff.Color = *f.Color
		eff.Sources[KeyColor] = SourceFile
	}
	return nil
}

// applyEnv overlays environment overrides (design D2). Only SHERLOG_PORT exists;
// it takes precedence over file and default. An empty value is treated as unset.
func applyEnv(eff *Effective) {
	if v := os.Getenv(envPort); v != "" {
		// An invalid SHERLOG_PORT is left to the daemon's bind to reject with an
		// actionable message; config does not gate the env override so the existing
		// SHERLOG_PORT behavior is unchanged.
		eff.Port = v
		eff.Sources[KeyPort] = SourceEnv
	}
}
