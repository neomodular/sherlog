package config

import (
	"os"
	"strings"
	"testing"
)

// TestLoadAbsentFileYieldsDefaults covers the no-config-file scenario: every
// effective value equals the built-in default and every source is default
// (configuration spec: "No config file").
func TestLoadAbsentFileYieldsDefaults(t *testing.T) {
	t.Setenv(envPort, "") // ensure no env override leaks in
	root := t.TempDir()

	eff, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	want := Default()
	if eff.Port != want.Port || eff.FloodKeep != want.FloodKeep ||
		eff.AwaitDebounceSeconds != want.AwaitDebounceSeconds ||
		eff.AwaitMaxTimeoutSeconds != want.AwaitMaxTimeoutSeconds ||
		eff.RetentionDays != want.RetentionDays ||
		eff.Verbosity != want.Verbosity || eff.Color != want.Color {
		t.Errorf("absent file did not yield defaults: %+v", eff)
	}
	for _, k := range Keys {
		if eff.Sources[k] != SourceDefault {
			t.Errorf("source[%s] = %q, want default", k, eff.Sources[k])
		}
	}
}

// TestLoadUnknownKeyFails covers the typo scenario: an unknown key fails loading
// with the offending name surfaced (configuration spec: "Typo in config").
func TestLoadUnknownKeyFails(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root, `{"flod_keep": 50}`)

	_, err := Load(root)
	if err == nil {
		t.Fatal("Load accepted an unknown key, want error")
	}
	if !strings.Contains(err.Error(), "flod_keep") {
		t.Errorf("error does not name the unknown key: %v", err)
	}
}

// TestLoadFileValues covers file-sourced overrides for every knob with correct
// source attribution.
func TestLoadFileValues(t *testing.T) {
	t.Setenv(envPort, "")
	root := t.TempDir()
	writeRaw(t, root, `{
		"port": "3000",
		"flood_keep": 50,
		"await_debounce_seconds": 5,
		"await_max_timeout_seconds": 900,
		"retention_days": 30,
		"verbosity": "minimal",
		"color": "never"
	}`)

	eff, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if eff.Port != "3000" || eff.FloodKeep != 50 || eff.AwaitDebounceSeconds != 5 ||
		eff.AwaitMaxTimeoutSeconds != 900 || eff.RetentionDays != 30 ||
		eff.Verbosity != VerbosityMinimal || eff.Color != ColorNever {
		t.Errorf("file values not applied: %+v", eff)
	}
	for _, k := range Keys {
		if eff.Sources[k] != SourceFile {
			t.Errorf("source[%s] = %q, want file", k, eff.Sources[k])
		}
	}
}

// TestEnvWinsOverFile covers precedence env > file > default (configuration spec:
// "Env wins over file"): SHERLOG_PORT overrides a file port and is marked env.
func TestEnvWinsOverFile(t *testing.T) {
	root := t.TempDir()
	writeRaw(t, root, `{"port": "3000"}`)
	t.Setenv(envPort, "4000")

	eff, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if eff.Port != "4000" {
		t.Errorf("port = %q, want 4000 (env wins)", eff.Port)
	}
	if eff.Sources[KeyPort] != SourceEnv {
		t.Errorf("port source = %q, want env", eff.Sources[KeyPort])
	}
}

// TestRangeValidation covers each range/enum boundary rejected at load time.
func TestRangeValidation(t *testing.T) {
	cases := map[string]string{
		"flood_keep too low":   `{"flood_keep": 0}`,
		"flood_keep too high":  `{"flood_keep": 1001}`,
		"debounce too high":    `{"await_debounce_seconds": 31}`,
		"debounce negative":    `{"await_debounce_seconds": -1}`,
		"max timeout too low":  `{"await_max_timeout_seconds": 29}`,
		"max timeout too high": `{"await_max_timeout_seconds": 3601}`,
		"retention negative":   `{"retention_days": -1}`,
		"verbosity invalid":    `{"verbosity": "loud"}`,
		"color invalid":        `{"color": "rainbow"}`,
		"port invalid":         `{"port": "0"}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Setenv(envPort, "")
			root := t.TempDir()
			writeRaw(t, root, body)
			if _, err := Load(root); err == nil {
				t.Errorf("Load accepted out-of-range config %q", body)
			}
		})
	}
}

// TestSetWritesAndPreservesOthers covers `config set`: a knob is written, other
// keys are preserved, and a re-Load reflects the change with source file
// (configuration spec: "Setting a knob").
func TestSetWritesAndPreservesOthers(t *testing.T) {
	t.Setenv(envPort, "")
	root := t.TempDir()

	if err := Set(root, KeyVerbosity, VerbosityMinimal); err != nil {
		t.Fatalf("Set verbosity: %v", err)
	}
	if err := Set(root, KeyFloodKeep, "50"); err != nil {
		t.Fatalf("Set flood_keep: %v", err)
	}

	eff, err := Load(root)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if eff.FloodKeep != 50 || eff.Sources[KeyFloodKeep] != SourceFile {
		t.Errorf("flood_keep not set: %+v", eff)
	}
	// The earlier verbosity set must survive the second set (other keys preserved).
	if eff.Verbosity != VerbosityMinimal || eff.Sources[KeyVerbosity] != SourceFile {
		t.Errorf("verbosity not preserved across set: %+v", eff)
	}
}

// TestSetRejectsInvalidLeavesFileUnchanged covers the rejection scenario: an
// invalid value fails and the file is not modified (configuration spec:
// "Rejecting an invalid value").
func TestSetRejectsInvalidLeavesFileUnchanged(t *testing.T) {
	root := t.TempDir()
	if err := Set(root, KeyFloodKeep, "50"); err != nil {
		t.Fatalf("seed set: %v", err)
	}
	before, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatalf("read seeded config: %v", err)
	}

	err = Set(root, KeyVerbosity, "loud")
	if err == nil {
		t.Fatal("Set accepted invalid verbosity, want error")
	}
	if !strings.Contains(err.Error(), VerbosityDetective) || !strings.Contains(err.Error(), VerbosityMinimal) {
		t.Errorf("error does not list allowed values: %v", err)
	}

	after, err := os.ReadFile(Path(root))
	if err != nil {
		t.Fatalf("read config after rejected set: %v", err)
	}
	if string(before) != string(after) {
		t.Errorf("rejected set modified the file:\nbefore=%s\nafter=%s", before, after)
	}
}

// TestSetUnknownKey covers a typo'd key on set: rejected with the name surfaced.
func TestSetUnknownKey(t *testing.T) {
	root := t.TempDir()
	err := Set(root, "flod_keep", "50")
	if err == nil {
		t.Fatal("Set accepted unknown key")
	}
	if !strings.Contains(err.Error(), "flod_keep") {
		t.Errorf("error does not name the unknown key: %v", err)
	}
}

// TestValueString round-trips every knob's display string.
func TestValueString(t *testing.T) {
	eff := Default()
	for _, k := range Keys {
		if _, err := eff.ValueString(k); err != nil {
			t.Errorf("ValueString(%q): %v", k, err)
		}
	}
	if _, err := eff.ValueString("nope"); err == nil {
		t.Error("ValueString accepted an unknown key")
	}
}

func writeRaw(t *testing.T, root, body string) {
	t.Helper()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(Path(root), []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}
