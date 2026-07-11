package daemon

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"

	"github.com/neomodular/sherlog/internal/store"
)

// maxProbeLineScan bounds a single scanned line when counting a probe file's lines
// (harden-detective-gates D-G), so a minified bundle with one enormous line cannot
// exhaust the scanner. Source probe locations are ordinary text files; the cap is
// generous.
const maxProbeLineScan = 1 << 20 // 1 MiB per line

// resolveProbePath resolves a probe's file against the session cwd (D-G): an
// absolute path is used as-is, a relative one is joined onto the session's stored
// cwd. The daemon owns investigation state (D6) and alone reliably knows the session
// cwd — the MCP process's cwd is not guaranteed to match — so the location check
// runs here rather than client-side.
func resolveProbePath(cwd, file string) string {
	if filepath.IsAbs(file) {
		return file
	}
	return filepath.Join(cwd, file)
}

// validateProbeLocation verifies a probe points at a real source line before it is
// registered (D-G): the resolved file must exist and be a regular file, and line
// must fall within [1, line-count]. A miss is an actionable error naming the
// resolved path, never a stored fiction — the cleanup gate cannot afford a probe it
// can never find (design: no override flag). Returns nil when the location checks
// out.
func validateProbeLocation(sess *store.Session, p store.Probe) error {
	resolved := resolveProbePath(sess.CWD, p.File)

	info, err := os.Stat(resolved)
	if err != nil {
		return fmt.Errorf("probe file not found at %s — register the probe at a source file path that exists relative to the session cwd", resolved)
	}
	if info.IsDir() {
		return fmt.Errorf("probe path %s is a directory, not a source file", resolved)
	}

	total, err := countFileLines(resolved)
	if err != nil {
		return fmt.Errorf("cannot read probe file %s: %w", resolved, err)
	}
	if p.Line < 1 || p.Line > total {
		return fmt.Errorf("probe line %d is out of range for %s, which has %d lines", p.Line, resolved, total)
	}
	return nil
}

// countFileLines returns the number of lines in a file. A final line without a
// trailing newline still counts (bufio.ScanLines semantics), so a 120-line file
// reports 120 whether or not it ends in a newline.
func countFileLines(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), maxProbeLineScan)
	n := 0
	for sc.Scan() {
		n++
	}
	if err := sc.Err(); err != nil {
		return 0, err
	}
	return n, nil
}
