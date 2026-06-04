package store

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const (
	stateFile = "state.json"
	logsFile  = "logs.jsonl"
)

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

// replayLogs reads logs.jsonl line by line and feeds each event back through the
// flood buffers, reconstructing the bounded in-memory view that existed before
// the restart (D8). A missing logs file is fine (a session may have no events).
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
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
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
	return nil
}
