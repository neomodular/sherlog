package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestResolutionPersistsAndSurvivesRestart covers session-state scenario "Solved
// case records its resolution": closing with a root cause persists it, it survives
// a daemon restart, and session reads return it.
func TestResolutionPersistsAndSurvivesRestart(t *testing.T) {
	root := t.TempDir()
	s1, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s1: %v", err)
	}
	sess, _, _ := s1.CreateSession("discount totals wrong", "/repo")
	s1.SetHypotheses(sess.ID, []string{"float rounding", "cache stale"})
	s1.UpdateHypothesis(sess.ID, "h1", HypothesisConfirmed, "p1 showed .005 rounding")

	res := &Resolution{
		RootCause:             "float rounding in discount calc",
		FixSummary:            "switched to integer cents",
		ConfirmedHypothesisID: "h1",
	}
	unremoved, err := s1.CloseSessionWithResolution(sess.ID, res)
	if err != nil {
		t.Fatalf("CloseSessionWithResolution: %v", err)
	}
	if len(unremoved) != 0 {
		t.Errorf("no probes registered, expected none unremoved: %+v", unremoved)
	}

	// Read back from the same store.
	got, err := s1.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Resolution == nil {
		t.Fatalf("resolution not recorded")
	}
	if got.Resolution.RootCause != res.RootCause || got.Resolution.FixSummary != res.FixSummary {
		t.Errorf("resolution fields lost: %+v", got.Resolution)
	}
	if got.Resolution.ConfirmedHypothesisID != "h1" {
		t.Errorf("confirmed hypothesis id lost: %+v", got.Resolution)
	}
	// ClosedAt on the resolution must match the session close time.
	if got.ClosedAt == nil || !got.Resolution.ClosedAt.Equal(*got.ClosedAt) {
		t.Errorf("resolution ClosedAt should match session ClosedAt: res=%v sess=%v",
			got.Resolution.ClosedAt, got.ClosedAt)
	}

	// Restart: a fresh store over the same root must recover the resolution.
	s2, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New s2 (restart): %v", err)
	}
	recovered, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after restart: %v", err)
	}
	if recovered.Resolution == nil || recovered.Resolution.RootCause != res.RootCause {
		t.Errorf("resolution not recovered after restart: %+v", recovered.Resolution)
	}
}

// TestCloseUnsolvedHasNoResolution covers session-state scenario "Unsolved close":
// closing without resolution fields (or with an all-empty resolution) records the
// case as closed-unsolved with a nil Resolution.
func TestCloseUnsolvedHasNoResolution(t *testing.T) {
	s := newTestStore(t)

	// Plain CloseSession: no resolution.
	a, _, _ := s.CreateSession("unsolved a", "/repo/a")
	if _, err := s.CloseSession(a.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	gotA, _ := s.GetSession(a.ID)
	if gotA.ClosedAt == nil {
		t.Errorf("session should be closed")
	}
	if gotA.Resolution != nil {
		t.Errorf("unsolved close must leave Resolution nil: %+v", gotA.Resolution)
	}

	// CloseSessionWithResolution with an all-empty resolution is also unsolved.
	b, _, _ := s.CreateSession("unsolved b", "/repo/b")
	if _, err := s.CloseSessionWithResolution(b.ID, &Resolution{}); err != nil {
		t.Fatalf("CloseSessionWithResolution empty: %v", err)
	}
	gotB, _ := s.GetSession(b.ID)
	if gotB.Resolution != nil {
		t.Errorf("all-empty resolution must be treated as unsolved: %+v", gotB.Resolution)
	}
}

// TestCloseResolutionIdempotent verifies re-closing a solved case does not alter
// the recorded resolution (CloseSession delegates to the resolution path).
func TestCloseResolutionIdempotent(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("idempotent", "/repo")
	if _, err := s.CloseSessionWithResolution(sess.ID, &Resolution{RootCause: "first"}); err != nil {
		t.Fatalf("first close: %v", err)
	}
	// A second close (with a different resolution, or none) must not overwrite.
	if _, err := s.CloseSessionWithResolution(sess.ID, &Resolution{RootCause: "second"}); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if _, err := s.CloseSession(sess.ID); err != nil {
		t.Fatalf("plain re-close: %v", err)
	}
	got, _ := s.GetSession(sess.ID)
	if got.Resolution == nil || got.Resolution.RootCause != "first" {
		t.Errorf("re-close overwrote the original resolution: %+v", got.Resolution)
	}
}

// TestOldStateFileLoadsWithoutResolution covers session-state requirement "Older
// state files without the field SHALL load unchanged": a state.json written before
// the Resolution field existed (no "resolution" key at all) recovers with a nil
// Resolution and is otherwise intact.
func TestOldStateFileLoadsWithoutResolution(t *testing.T) {
	root := t.TempDir()
	id := "oldsess1"
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	// A pre-resolution state.json: no "resolution" key, closed session.
	legacy := `{
  "id": "oldsess1",
  "description": "legacy bug",
  "cwd": "/repo/legacy",
  "created_at": "2024-01-02T03:04:05Z",
  "closed_at": "2024-01-02T04:00:00Z",
  "hypotheses": [],
  "probes": [],
  "runs": []
}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	s, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("New over legacy root: %v", err)
	}
	got, err := s.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession legacy: %v", err)
	}
	if got.Description != "legacy bug" || got.ClosedAt == nil {
		t.Errorf("legacy fields not loaded: %+v", got)
	}
	if got.Resolution != nil {
		t.Errorf("legacy session must load with nil Resolution: %+v", got.Resolution)
	}
}

// TestResolutionJSONOmittedWhenNil guards the backward-compat shape: an open
// session (nil Resolution) marshals without a "resolution" key, so older binaries
// reading the file see no unexpected field (Migration Plan: additive field).
func TestResolutionJSONOmittedWhenNil(t *testing.T) {
	sess := Session{ID: "x", Description: "d"}
	data, err := json.Marshal(sess)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if _, present := m["resolution"]; present {
		t.Errorf("nil Resolution must be omitted from JSON, got: %s", data)
	}
}
