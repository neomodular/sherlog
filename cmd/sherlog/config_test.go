package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/neomodular/sherlog/internal/config"
)

// TestConfigSetThenListShowsFileSource covers `set` then `list` over a temp config
// root: the value is written and list reports it with source file (configuration
// spec: "Setting a knob").
func TestConfigSetThenListShowsFileSource(t *testing.T) {
	t.Setenv("SHERLOG_PORT", "") // keep env out of the precedence under test
	root := t.TempDir()

	var setBuf bytes.Buffer
	if err := configSet(&setBuf, root, config.KeyFloodKeep, "50"); err != nil {
		t.Fatalf("configSet: %v", err)
	}

	var listBuf bytes.Buffer
	if err := configList(&listBuf, root); err != nil {
		t.Fatalf("configList: %v", err)
	}
	out := listBuf.String()
	// flood_keep row must show the new value and the file source.
	line := rowFor(t, out, config.KeyFloodKeep)
	if !strings.Contains(line, "50") || !strings.Contains(line, string(config.SourceFile)) {
		t.Errorf("flood_keep row = %q, want value 50 and source file", line)
	}
	// Untouched keys still show their default source.
	if l := rowFor(t, out, config.KeyVerbosity); !strings.Contains(l, string(config.SourceDefault)) {
		t.Errorf("verbosity row = %q, want source default", l)
	}
}

// TestConfigGet prints a bare value, scriptable.
func TestConfigGet(t *testing.T) {
	t.Setenv("SHERLOG_PORT", "")
	root := t.TempDir()
	if err := configSet(&bytes.Buffer{}, root, config.KeyRetentionDays, "30"); err != nil {
		t.Fatalf("seed set: %v", err)
	}
	var buf bytes.Buffer
	if err := configGet(&buf, root, config.KeyRetentionDays); err != nil {
		t.Fatalf("configGet: %v", err)
	}
	if got := strings.TrimSpace(buf.String()); got != "30" {
		t.Errorf("get retention_days = %q, want 30", got)
	}
}

// TestConfigSetValidation is table-driven over set inputs: each invalid case must
// fail and (when seeded) leave the file unchanged; valid cases must succeed
// (configuration spec: "Rejecting an invalid value").
func TestConfigSetValidation(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		value   string
		wantErr bool
	}{
		{"valid flood_keep", config.KeyFloodKeep, "1000", false},
		{"flood_keep too high", config.KeyFloodKeep, "1001", true},
		{"flood_keep non-int", config.KeyFloodKeep, "abc", true},
		{"valid verbosity", config.KeyVerbosity, config.VerbosityMinimal, false},
		{"invalid verbosity", config.KeyVerbosity, "loud", true},
		{"valid color", config.KeyColor, config.ColorNever, false},
		{"invalid color", config.KeyColor, "rainbow", true},
		{"valid debounce 0", config.KeyAwaitDebounceSeconds, "0", false},
		{"debounce too high", config.KeyAwaitDebounceSeconds, "31", true},
		{"max timeout too low", config.KeyAwaitMaxTimeoutSeconds, "29", true},
		{"retention negative", config.KeyRetentionDays, "-1", true},
		{"valid port", config.KeyPort, "3000", false},
		{"port out of range", config.KeyPort, "70000", true},
		{"unknown key", "bogus", "x", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("SHERLOG_PORT", "")
			root := t.TempDir()
			err := configSet(&bytes.Buffer{}, root, tc.key, tc.value)
			if tc.wantErr && err == nil {
				t.Errorf("configSet(%q,%q) = nil, want error", tc.key, tc.value)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("configSet(%q,%q) = %v, want success", tc.key, tc.value, err)
			}
		})
	}
}

// TestCmdConfigDispatch covers the top-level dispatch and arg checking through
// cmdConfig with HOME pointed at a temp dir (so DefaultRoot resolves there).
func TestCmdConfigDispatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home) // Windows home resolution
	t.Setenv("SHERLOG_PORT", "")

	var buf bytes.Buffer
	if err := cmdConfig(&buf, []string{"list"}); err != nil {
		t.Fatalf("cmdConfig list: %v", err)
	}
	if !strings.Contains(buf.String(), config.KeyFloodKeep) {
		t.Errorf("list output missing flood_keep:\n%s", buf.String())
	}

	if err := cmdConfig(&bytes.Buffer{}, nil); err == nil {
		t.Error("cmdConfig with no args should error")
	}
	if err := cmdConfig(&bytes.Buffer{}, []string{"get"}); err == nil {
		t.Error("config get with no key should error")
	}
	if err := cmdConfig(&bytes.Buffer{}, []string{"set", "flood_keep"}); err == nil {
		t.Error("config set with no value should error")
	}
}

// rowFor returns the table row for key from list output, failing if absent.
func rowFor(t *testing.T, out, key string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), key) {
			return line
		}
	}
	t.Fatalf("no row for key %q in:\n%s", key, out)
	return ""
}
