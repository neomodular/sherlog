package daemon

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/neomodular/sherlog/internal/store"
)

// writeFile writes content to rel under dir, creating parent directories. Shared by
// the walker and endpoint tests; every blast-radius test builds a throwaway temp tree
// (never the repo, per the house rule).
func writeFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	writeBytes(t, dir, rel, []byte(content))
}

// writeBytes is writeFile for exact byte content (binary sniff, oversize cases).
func writeBytes(t *testing.T, dir, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// hitKeys renders a hit set as sorted-independent "file:line" strings for comparison.
func hitKeys(hits []store.BlastHit) map[string]bool {
	m := make(map[string]bool, len(hits))
	for _, h := range hits {
		m[fmt.Sprintf("%s:%d", h.File, h.Line)] = true
	}
	return m
}

// TestWalkBlastRadius covers the inclusion/exclusion matrix (blast-radius spec: "The
// search is bounded"): matches are recorded cwd-relative across subdirectories, and
// .git internals, NUL-sniffed binaries, and oversized files never produce hits.
func TestWalkBlastRadius(t *testing.T) {
	cases := []struct {
		name    string
		build   func(t *testing.T, dir string)
		pattern string
		want    []string // "relpath:line", order-independent
	}{
		{
			name: "matches across subdirectories, recorded cwd-relative with forward slashes",
			build: func(t *testing.T, dir string) {
				writeFile(t, dir, "src/a.js", "clean line\nreadToken()\n")
				writeFile(t, dir, "src/b.js", "readToken()\nother\n")
				writeFile(t, dir, "pkg/c.go", "x := readToken()\n")
			},
			pattern: `readToken\(\)`,
			want:    []string{"src/a.js:2", "src/b.js:1", "pkg/c.go:1"},
		},
		{
			name: "skips .git internals",
			build: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.js", "readToken()\n")
				writeFile(t, dir, ".git/config", "readToken()\n")
				writeFile(t, dir, ".git/objects/pack/readToken.idx", "readToken()\n")
			},
			pattern: `readToken\(\)`,
			want:    []string{"a.js:1"},
		},
		{
			name: "skips binary files sniffed by a leading NUL byte",
			build: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.js", "readToken()\n")
				writeBytes(t, dir, "blob.bin", []byte("readToken()\x00readToken()\n"))
			},
			pattern: `readToken\(\)`,
			want:    []string{"a.js:1"},
		},
		{
			name: "skips files over the size cap",
			build: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.js", "readToken()\n")
				oversized := append([]byte("readToken()\n"), bytes.Repeat([]byte("x"), blastMaxFileBytes+1)...)
				writeBytes(t, dir, "big.js", oversized)
			},
			pattern: `readToken\(\)`,
			want:    []string{"a.js:1"},
		},
		{
			name: "no matches yields no hits",
			build: func(t *testing.T, dir string) {
				writeFile(t, dir, "a.js", "nothing here\n")
			},
			pattern: `readToken\(\)`,
			want:    nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			tc.build(t, dir)
			re := regexp.MustCompile(tc.pattern)

			hits, truncated, err := walkBlastRadius(dir, re)
			if err != nil {
				t.Fatalf("walkBlastRadius: %v", err)
			}
			if truncated {
				t.Errorf("unexpected truncation for %d hits", len(hits))
			}
			got := hitKeys(hits)
			if len(got) != len(tc.want) {
				t.Fatalf("hit set = %v, want %v", got, tc.want)
			}
			for _, wantHit := range tc.want {
				if !got[wantHit] {
					t.Errorf("missing hit %s; got %v", wantHit, got)
				}
			}
		})
	}
}

// TestWalkBlastRadiusTruncation covers the hit cap (blast-radius spec: "Truncated scan
// disclosed"): a pattern with more matches than the cap yields exactly the cap's worth
// of hits with truncated=true.
func TestWalkBlastRadiusTruncation(t *testing.T) {
	dir := t.TempDir()
	var b strings.Builder
	for i := 0; i < blastHitCap+50; i++ {
		b.WriteString("readToken()\n")
	}
	writeFile(t, dir, "many.js", b.String())

	hits, truncated, err := walkBlastRadius(dir, regexp.MustCompile(`readToken`))
	if err != nil {
		t.Fatalf("walkBlastRadius: %v", err)
	}
	if !truncated {
		t.Error("want truncated=true when matches exceed the hit cap")
	}
	if len(hits) != blastHitCap {
		t.Errorf("recorded %d hits, want the cap of %d", len(hits), blastHitCap)
	}
}

// TestTrimExcerpt covers excerpt normalization: whitespace trimmed, long lines cut on a
// rune boundary (never mid-multibyte) with an ellipsis, short lines untouched.
func TestTrimExcerpt(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // exact when short; checked structurally when trimmed
		trim bool
	}{
		{name: "short line whitespace-trimmed unchanged", in: "  readToken()  ", want: "readToken()"},
		{name: "ascii line over cap trimmed with ellipsis", in: "   " + strings.Repeat("A", 400), trim: true},
		{name: "multibyte line over cap trimmed on a rune boundary", in: strings.Repeat("é", 400), trim: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := trimExcerpt(tc.in)
			if !tc.trim {
				if got != tc.want {
					t.Errorf("trimExcerpt(%q) = %q, want %q", tc.in, got, tc.want)
				}
				return
			}
			if !utf8.ValidString(got) {
				t.Errorf("trimmed excerpt is not valid UTF-8: %q", got)
			}
			if !strings.HasSuffix(got, "…") {
				t.Errorf("trimmed excerpt should end with an ellipsis, got %q", got)
			}
			if n := utf8.RuneCountInString(got); n != blastExcerptRunes+1 {
				t.Errorf("trimmed excerpt = %d runes, want %d (cap + ellipsis)", n, blastExcerptRunes+1)
			}
			if strings.HasPrefix(got, " ") {
				t.Errorf("excerpt should be whitespace-trimmed, got %q", got)
			}
		})
	}
}

// TestWalkBlastRadiusSkipsSymlinks confirms symlinks are never followed (blast-radius
// spec bound): a symlinked file is not scanned (so its target is not double-counted),
// and a symlink to a file outside the tree yields no hit.
func TestWalkBlastRadiusSkipsSymlinks(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "real.js", "readToken()\n")

	// A symlink alongside the real file: WalkDir must not scan the link, so the match is
	// counted once (from real.js), never twice.
	if err := os.Symlink(filepath.Join(dir, "real.js"), filepath.Join(dir, "link.js")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}
	// A symlink pointing outside the walked tree must not be followed at all.
	outside := t.TempDir()
	writeFile(t, outside, "external.js", "readToken()\n")
	if err := os.Symlink(filepath.Join(outside, "external.js"), filepath.Join(dir, "external-link.js")); err != nil {
		t.Skipf("symlinks unsupported on this platform: %v", err)
	}

	hits, truncated, err := walkBlastRadius(dir, regexp.MustCompile(`readToken`))
	if err != nil {
		t.Fatalf("walkBlastRadius: %v", err)
	}
	if truncated {
		t.Error("unexpected truncation")
	}
	if len(hits) != 1 || hits[0].File != "real.js" {
		t.Fatalf("want exactly one hit at real.js (symlinks skipped), got %+v", hits)
	}
}
