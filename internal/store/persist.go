package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

const (
	stateFile = "state.json"
	logsFile  = "logs.jsonl"
)

// adoptMarker is the append-only record of a pre-run adoption written to
// logs.jsonl (design D2): replay re-applies it in order after the events it
// references are loaded, so attribution survives restart without rewriting any
// existing line. Its window (From, To] mirrors the live adoption predicate.
type adoptMarker struct {
	Run  string    `json:"run"`
	From time.Time `json:"from"` // exclusive lower bound (last run boundary or cap)
	To   time.Time `json:"to"`   // inclusive upper bound (adoption time)
}

// logLine is the union shape used to read logs.jsonl: a plain event line leaves
// Adopt nil; an adoption marker line carries Adopt and no event fields. The two
// shapes share no JSON keys, which is only what lets the CURRENT two-pass reader
// tell a marker apart from an event. It is NOT a backward-compat guarantee: an
// older event-only binary would Unmarshal a marker line into a zero-value LogEvent
// with no error and mis-load it as a phantom empty orphan, not skip it (design D2).
// Downgrade-after-upgrade is therefore unsupported.
type logLine struct {
	Adopt *adoptMarker `json:"adopt,omitempty"`
	// Embedded event fields are decoded into a LogEvent separately; see replayLogs.
}

// sessionDir is the on-disk directory for one session: ~/.sherlog/sessions/<id>/ (D5).
func (s *Store) sessionDir(id string) string {
	return filepath.Join(s.root, "sessions", id)
}

// writeState rewrites a session's state.json atomically: marshal to a temp file
// in the same directory, fsync, then rename over the target. Rename is atomic on
// the same filesystem, so a crash mid-write never leaves a half-written
// state.json — the file is either the old or new version (D5).
func (s *Store) writeState(sess *Session) error {
	dir := s.sessionDir(sess.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir %q: %w", dir, err)
	}

	data, err := json.MarshalIndent(sess, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state for %q: %w", sess.ID, err)
	}

	tmp, err := os.CreateTemp(dir, stateFile+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp state file: %w", err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup if we fail before the rename succeeds.
	defer func() { _ = os.Remove(tmpName) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("sync temp state file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp state file: %w", err)
	}

	if err := os.Rename(tmpName, filepath.Join(dir, stateFile)); err != nil {
		return fmt.Errorf("rename state file for %q: %w", sess.ID, err)
	}
	return nil
}

// appendLog appends one event as a JSON line to the session's logs.jsonl. The
// file is the durable, unbounded record of ingest; flood control bounds only the
// in-memory copy (D5, D8). Recovery replays this file to rebuild the buffers.
func (s *Store) appendLog(sessionID string, ev LogEvent) error {
	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir %q: %w", dir, err)
	}

	line, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal log event: %w", err)
	}

	f, err := os.OpenFile(filepath.Join(dir, logsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open logs file for %q: %w", sessionID, err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append log event for %q: %w", sessionID, err)
	}
	return nil
}

// appendAdoptMarker appends one adoption marker line to the session's logs.jsonl
// (design D2). It takes entry.appendMu so the marker serializes with event
// appends on the same per-session lock, guaranteeing every adopted event's line
// is already durable before the marker line: Ingest acquires appendMu while still
// holding s.mu, so any orphan recorded in memory before this run opened has also
// already acquired (or holds) appendMu, and this marker waits behind it. Without
// that ordering, a marker flushed ahead of a still-pending event line would, on
// restart, apply before the event loads and leave it an orphan (the fast-repro
// vs. await race this change targets). Callers hold s.mu.
func (s *Store) appendAdoptMarker(entry *sessionEntry, m adoptMarker) error {
	sessionID := entry.session.ID
	dir := s.sessionDir(sessionID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create session dir %q: %w", dir, err)
	}

	line, err := json.Marshal(logLine{Adopt: &m})
	if err != nil {
		return fmt.Errorf("marshal adoption marker: %w", err)
	}

	entry.appendMu.Lock()
	defer entry.appendMu.Unlock()

	f, err := os.OpenFile(filepath.Join(dir, logsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open logs file for %q: %w", sessionID, err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return fmt.Errorf("append adoption marker for %q: %w", sessionID, err)
	}
	return nil
}

// recover loads every session under root by replaying state.json and logs.jsonl.
// Missing root or sessions dir is not an error (fresh install). A session
// directory without a readable state.json is skipped with no fatal error so one
// corrupt session can never block startup of the rest (D5: state survives
// restart). The returned entries are keyed by session ID.
func (s *Store) recover() (map[string]*sessionEntry, error) {
	entries := make(map[string]*sessionEntry)

	sessionsRoot := filepath.Join(s.root, "sessions")
	dirEntries, err := os.ReadDir(sessionsRoot)
	if errors.Is(err, os.ErrNotExist) {
		return entries, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	for _, de := range dirEntries {
		if !de.IsDir() {
			continue
		}
		id := de.Name()
		sess, err := s.readState(id)
		if err != nil {
			// Skip unreadable sessions rather than aborting recovery.
			continue
		}
		entry := &sessionEntry{session: sess, floods: make(map[floodKey]*floodBuffer)}
		if err := s.replayLogs(id, entry); err != nil {
			return nil, fmt.Errorf("replay logs for %q: %w", id, err)
		}
		entries[id] = entry
	}
	return entries, nil
}

func (s *Store) readState(id string) (*Session, error) {
	data, err := os.ReadFile(filepath.Join(s.sessionDir(id), stateFile))
	if err != nil {
		return nil, fmt.Errorf("read state file: %w", err)
	}
	var sess Session
	if err := json.Unmarshal(data, &sess); err != nil {
		return nil, fmt.Errorf("unmarshal state file: %w", err)
	}
	return &sess, nil
}

// replayLogs reads logs.jsonl and reconstructs the bounded in-memory view that
// existed before the restart (D8). It runs in two passes: first every event is
// loaded into its flood buffer, then every adoption marker is applied in file
// order (D2). Loading all events before any marker makes replay independent of
// the on-disk interleaving of events and markers, so a marker flushed ahead of an
// adopted event's line (a live ingest/open race) can never leave that event an
// orphan after restart — replay reproduces the live attribution regardless. A
// missing logs file is fine (a session may have no events).
func (s *Store) replayLogs(id string, entry *sessionEntry) error {
	f, err := os.Open(filepath.Join(s.sessionDir(id), logsFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open logs file: %w", err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	// Probe bodies can be large; raise the per-line limit well above the default 64KB.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)

	var markers []adoptMarker // applied after all events load, in file order
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		// Adoption markers and events share the file; a marker line carries the
		// "adopt" key and no event fields. Defer markers to the second pass.
		var ll logLine
		if err := json.Unmarshal(line, &ll); err == nil && ll.Adopt != nil {
			markers = append(markers, *ll.Adopt)
			continue
		}
		var ev LogEvent
		if err := json.Unmarshal(line, &ev); err != nil {
			// A torn final line (crash mid-append) is tolerable; skip it.
			continue
		}
		entry.recordEvent(ev, s.floodN)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return fmt.Errorf("scan logs file: %w", err)
	}

	// Second pass: every event is now loaded, so each marker adopts exactly the
	// orphans its window covers, reproducing the live adoption (D2).
	for _, m := range markers {
		entry.adoptOrphans(m.Run, m.From, m.To)
	}
	return nil
}
