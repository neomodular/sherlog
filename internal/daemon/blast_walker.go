package daemon

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/neomodular/sherlog/internal/store"
)

// The bounded sibling-search walker (add-blast-radius D-A/D-B/D-G). The daemon is
// the search EXECUTOR: it compiles the agent-authored pattern with the stdlib RE2
// engine and walks the session cwd itself, so the hit list is a recorded fact the
// agent can neither pad nor prune. The bounds below mirror flood control's
// philosophy — bound the work, always disclose the bound (Truncated). The walk
// touches only the filesystem and holds no store lock, so a long scan never blocks
// /log/ ingest or an open await_run (D-G).
const (
	// blastHitCap stops the walk after this many recorded hits and sets Truncated,
	// short-circuiting a pathological pattern that would match everything (D-B).
	blastHitCap = 500
	// blastMaxFileBytes skips any file larger than 2 MiB: sibling source is ordinary
	// text; a giant blob is almost certainly generated or binary (D-B).
	blastMaxFileBytes = 2 << 20 // 2 MiB
	// blastBinarySniffBytes is how many leading bytes are checked for a NUL: a NUL in
	// the head marks the file binary and it is skipped (D-B).
	blastBinarySniffBytes = 8 << 10 // 8 KiB
	// blastMaxLineBytes caps a single scanned line so a minified one-line file within
	// the size cap cannot exhaust the scanner; set to the file cap so any line in an
	// eligible file fits.
	blastMaxLineBytes = blastMaxFileBytes
	// blastExcerptRunes trims each hit excerpt to roughly this many runes so a matched
	// minified line does not bloat state.json; disclosure, not the full line, is the point.
	blastExcerptRunes = 200
)

// walkBlastRadius searches file contents under cwd for re and returns the recorded
// hits, whether the hit cap truncated the scan, and any fatal walk error (D-A). It
// skips .git directories, symlinks, non-regular files, oversized files, and binary
// files (D-B). Paths are recorded cwd-relative with forward slashes so a hit compares
// equal to the cwd-relative probe file the false-coverage gate checks (store D-C).
// The walk takes no store lock (D-G): callers run it before touching the store.
func walkBlastRadius(cwd string, re *regexp.Regexp) (hits []store.BlastHit, truncated bool, err error) {
	walkErr := filepath.WalkDir(cwd, func(path string, d fs.DirEntry, entryErr error) error {
		if entryErr != nil {
			// An unreadable entry is skipped, never fatal: a permission-denied subtree
			// must not abort the whole scan. Skip the whole subtree when it is a directory.
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if len(hits) >= blastHitCap {
			truncated = true
			return filepath.SkipAll
		}
		if d.IsDir() {
			// Skip .git wholesale (D-B): VCS internals are never sibling source and would
			// match packed objects and refs as noise.
			if d.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		// Never follow symlinks (D-B). WalkDir does not descend a symlinked directory,
		// and a symlinked file must not be scanned either — it could point outside cwd.
		if d.Type()&fs.ModeSymlink != 0 || !d.Type().IsRegular() {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil // a file that vanished mid-walk is skipped, not fatal
		}
		if info.Size() > blastMaxFileBytes {
			return nil
		}
		fileHits, more := scanFile(cwd, path, re, blastHitCap-len(hits))
		hits = append(hits, fileHits...)
		if more || len(hits) >= blastHitCap {
			// Stop at the cap and disclose it (D-B): the agent is told to narrow the
			// pattern and re-run rather than trust a silently bounded list.
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil {
		return nil, false, fmt.Errorf("walk blast radius under %s: %w", cwd, walkErr)
	}
	return hits, truncated, nil
}

// scanFile records up to limit hits for re in one file and reports whether a further
// match existed beyond limit (so the caller can flag truncation). It skips a file
// whose leading bytes contain a NUL (binary sniff, D-B). A scan error (e.g. a line
// past the buffer cap) ends the file with what was found — a pathological file must
// never abort the surrounding walk. limit is always ≥ 0; a limit of 0 records nothing
// but still reports whether any match exists.
func scanFile(cwd, path string, re *regexp.Regexp, limit int) (hits []store.BlastHit, more bool) {
	f, err := os.Open(path)
	if err != nil {
		return nil, false
	}
	defer f.Close()

	// Binary sniff on the leading bytes (D-B). ReadFull yields ErrUnexpectedEOF/EOF for
	// files shorter than the sniff window, which is expected, not an error.
	head := make([]byte, blastBinarySniffBytes)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return nil, false
	}
	head = head[:n]
	if bytes.IndexByte(head, 0) >= 0 {
		return nil, false
	}

	rel, err := filepath.Rel(cwd, path)
	if err != nil {
		rel = path
	}
	rel = filepath.ToSlash(rel)

	// Rejoin the sniffed head with the rest of the file so no bytes are re-read or
	// skipped; the file cursor already sits past the head after ReadFull.
	sc := bufio.NewScanner(io.MultiReader(bytes.NewReader(head), f))
	sc.Buffer(make([]byte, 0, 64*1024), blastMaxLineBytes)
	line := 0
	for sc.Scan() {
		line++
		text := sc.Text()
		if !re.MatchString(text) {
			continue
		}
		if len(hits) >= limit {
			// One more match than the caller's remaining budget: report it so the walk
			// flags truncation, and stop scanning this file.
			return hits, true
		}
		hits = append(hits, store.BlastHit{File: rel, Line: line, Excerpt: trimExcerpt(text)})
	}
	return hits, false
}

// trimExcerpt normalizes and length-caps one matched line into a hit excerpt: leading
// and trailing whitespace stripped, then trimmed to ~blastExcerptRunes runes on a rune
// boundary (never mid-multibyte) with an ellipsis marking the cut.
func trimExcerpt(line string) string {
	s := strings.TrimSpace(line)
	if len(s) <= blastExcerptRunes {
		// Byte length ≤ cap implies rune length ≤ cap, so no truncation is possible.
		return s
	}
	runes := []rune(s)
	if len(runes) <= blastExcerptRunes {
		return s
	}
	return string(runes[:blastExcerptRunes]) + "…"
}
