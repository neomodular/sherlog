package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Keys lists every config key in display order, used by `config list` and to
// validate `config set`/`get` key names (single source of the key set, DRY).
var Keys = []string{
	KeyPort,
	KeyFloodKeep,
	KeyAwaitDebounceSeconds,
	KeyAwaitMaxTimeoutSeconds,
	KeyRetentionDays,
	KeyVerbosity,
	KeyColor,
}

// known reports whether key is a valid config key.
func known(key string) bool {
	for _, k := range Keys {
		if k == key {
			return true
		}
	}
	return false
}

// ValueString returns the effective value of key as a display string, matching
// the JSON form the file would carry. An unknown key is an error.
func (e Effective) ValueString(key string) (string, error) {
	switch key {
	case KeyPort:
		return e.Port, nil
	case KeyFloodKeep:
		return strconv.Itoa(e.FloodKeep), nil
	case KeyAwaitDebounceSeconds:
		return strconv.Itoa(e.AwaitDebounceSeconds), nil
	case KeyAwaitMaxTimeoutSeconds:
		return strconv.Itoa(e.AwaitMaxTimeoutSeconds), nil
	case KeyRetentionDays:
		return strconv.Itoa(e.RetentionDays), nil
	case KeyVerbosity:
		return e.Verbosity, nil
	case KeyColor:
		return e.Color, nil
	default:
		return "", unknownKeyError(key)
	}
}

// --- validation (design D3 ranges) ---

func validatePort(v string) error {
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > 65535 {
		return fmt.Errorf("invalid %s %q: want a TCP port 1–65535", KeyPort, v)
	}
	return nil
}

func validateFloodKeep(n int) error {
	if n < 1 || n > 1000 {
		return fmt.Errorf("invalid %s %d: want 1–1000", KeyFloodKeep, n)
	}
	return nil
}

func validateDebounce(n int) error {
	if n < 0 || n > 30 {
		return fmt.Errorf("invalid %s %d: want 0–30", KeyAwaitDebounceSeconds, n)
	}
	return nil
}

func validateMaxTimeout(n int) error {
	if n < 30 || n > 3600 {
		return fmt.Errorf("invalid %s %d: want 30–3600", KeyAwaitMaxTimeoutSeconds, n)
	}
	return nil
}

func validateRetentionDays(n int) error {
	if n < 0 {
		return fmt.Errorf("invalid %s %d: want >= 0 (0 keeps forever)", KeyRetentionDays, n)
	}
	return nil
}

func validateVerbosity(v string) error {
	if v != VerbosityDetective && v != VerbosityMinimal {
		return fmt.Errorf("invalid %s %q: want %s or %s", KeyVerbosity, v, VerbosityDetective, VerbosityMinimal)
	}
	return nil
}

func validateColor(v string) error {
	if v != ColorAuto && v != ColorAlways && v != ColorNever {
		return fmt.Errorf("invalid %s %q: want %s, %s, or %s", KeyColor, v, ColorAuto, ColorAlways, ColorNever)
	}
	return nil
}

func unknownKeyError(key string) error {
	return fmt.Errorf("unknown config key %q (known keys: %v)", key, Keys)
}

// Set parses and validates a string value for key, then writes it into the
// config file under root atomically (design D3). It reads the existing file (so
// other keys are preserved), applies the one change, and rewrites via temp+rename
// — the same durability pattern as state.json. An invalid key or value returns an
// error and leaves the file unchanged.
func Set(root, key, value string) error {
	if !known(key) {
		return unknownKeyError(key)
	}

	f, err := readFile(Path(root))
	if err != nil {
		return err
	}
	if f == nil {
		f = &file{}
	}

	if err := assign(f, key, value); err != nil {
		return err
	}
	return writeFile(root, f)
}

// assign parses value for key, validates it, and stores it on f. Parsing failure
// (e.g. a non-integer for flood_keep) is reported the same way as a range
// violation so `config set` errors are uniform.
func assign(f *file, key, value string) error {
	switch key {
	case KeyPort:
		if err := validatePort(value); err != nil {
			return err
		}
		f.Port = &value
	case KeyFloodKeep:
		n, err := parseInt(key, value)
		if err != nil {
			return err
		}
		if err := validateFloodKeep(n); err != nil {
			return err
		}
		f.FloodKeep = &n
	case KeyAwaitDebounceSeconds:
		n, err := parseInt(key, value)
		if err != nil {
			return err
		}
		if err := validateDebounce(n); err != nil {
			return err
		}
		f.AwaitDebounceSeconds = &n
	case KeyAwaitMaxTimeoutSeconds:
		n, err := parseInt(key, value)
		if err != nil {
			return err
		}
		if err := validateMaxTimeout(n); err != nil {
			return err
		}
		f.AwaitMaxTimeoutSeconds = &n
	case KeyRetentionDays:
		n, err := parseInt(key, value)
		if err != nil {
			return err
		}
		if err := validateRetentionDays(n); err != nil {
			return err
		}
		f.RetentionDays = &n
	case KeyVerbosity:
		if err := validateVerbosity(value); err != nil {
			return err
		}
		f.Verbosity = &value
	case KeyColor:
		if err := validateColor(value); err != nil {
			return err
		}
		f.Color = &value
	default:
		return unknownKeyError(key)
	}
	return nil
}

func parseInt(key, value string) (int, error) {
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: want an integer", key, value)
	}
	return n, nil
}

// writeFile rewrites config.json atomically: marshal, write a temp file in the
// same directory, fsync, then rename over the target (design D3, mirroring
// state.json). Rename on the same filesystem is atomic, so a crash mid-write
// never leaves a half-written config.
func writeFile(root string, f *file) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return fmt.Errorf("create config dir %q: %w", root, err)
	}

	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}
	data = append(data, '\n')

	tmp, err := os.CreateTemp(root, configFileName+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp config file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp config file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}
	if err := os.Rename(tmpName, filepath.Join(root, configFileName)); err != nil {
		return fmt.Errorf("rename config file: %w", err)
	}
	return nil
}
