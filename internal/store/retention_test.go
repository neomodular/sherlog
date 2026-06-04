package store

import (
	"os"
	"testing"
	"time"
)

// TestPruneOldClosedSession covers retention pruning of a stale closed case
// (configuration spec: "Old closed case pruned"): a session closed 45 days ago is
// removed from memory and disk and reported, with a 30-day window.
func TestPruneOldClosedSession(t *testing.T) {
	s := newTestStore(t)
	sess, _, err := s.CreateSession("old bug", "/tmp/a")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.CloseSession(sess.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	// Backdate the close to 45 days ago, beyond a 30-day window.
	backdateClosed(s, sess.ID, time.Now().Add(-45*24*time.Hour))

	cutoff := time.Now().Add(-30 * 24 * time.Hour)
	pruned, err := s.PruneClosedBefore(cutoff)
	if err != nil {
		t.Fatalf("PruneClosedBefore: %v", err)
	}
	if len(pruned) != 1 || pruned[0] != sess.ID {
		t.Fatalf("pruned = %v, want [%s]", pruned, sess.ID)
	}
	if _, err := s.GetSession(sess.ID); err == nil {
		t.Error("pruned session still present in memory")
	}
	if _, err := os.Stat(s.sessionDir(sess.ID)); !os.IsNotExist(err) {
		t.Errorf("session dir not removed from disk: stat err = %v", err)
	}
}

// TestPruneOpenSessionImmune covers open-session immunity (configuration spec:
// "Open session immune"): an open session older than the window is never pruned.
func TestPruneOpenSessionImmune(t *testing.T) {
	s := newTestStore(t)
	sess, _, err := s.CreateSession("long-open bug", "/tmp/b")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	// Backdate creation to 10 days ago; the session stays open (ClosedAt nil).
	s.mu.Lock()
	s.sessions[sess.ID].session.CreatedAt = time.Now().Add(-10 * 24 * time.Hour)
	s.mu.Unlock()

	// A 1-day window: an open session this old must still survive.
	pruned, err := s.PruneClosedBefore(time.Now().Add(-24 * time.Hour))
	if err != nil {
		t.Fatalf("PruneClosedBefore: %v", err)
	}
	if len(pruned) != 0 {
		t.Fatalf("open session pruned: %v", pruned)
	}
	if _, err := s.GetSession(sess.ID); err != nil {
		t.Errorf("open session removed: %v", err)
	}
}

// TestPruneKeepsRecentlyClosed confirms a closed session inside the window is kept.
func TestPruneKeepsRecentlyClosed(t *testing.T) {
	s := newTestStore(t)
	sess, _, err := s.CreateSession("recent bug", "/tmp/c")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := s.CloseSession(sess.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	// Closed 5 days ago; a 30-day window keeps it.
	backdateClosed(s, sess.ID, time.Now().Add(-5*24*time.Hour))

	pruned, err := s.PruneClosedBefore(time.Now().Add(-30 * 24 * time.Hour))
	if err != nil {
		t.Fatalf("PruneClosedBefore: %v", err)
	}
	if len(pruned) != 0 {
		t.Errorf("recently-closed session pruned: %v", pruned)
	}
}

// backdateClosed rewrites a session's ClosedAt directly in memory for retention
// tests, avoiding any dependence on wall-clock aging.
func backdateClosed(s *Store, id string, t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tt := t
	s.sessions[id].session.ClosedAt = &tt
}
