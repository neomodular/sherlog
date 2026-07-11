package store

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file covers the add-blast-radius delta specs (specs/blast-radius and the
// session-state additions): every store-side scenario there is exercised here as an
// acceptance test against the store — the gate, the annotation set-check, replace
// semantics, and persistence round-trip + legacy load.

// confirmCulprit brings a session to a confirmed board whose culprit probe points at
// culpritFile: three suspects, a predicted probe pc on h1 at culpritFile, a closed
// reproduced run, then a confirm of h1 citing pc. It mirrors confirmH1 but lets the
// caller pick the culprit file so the false-coverage gate can be exercised with
// absolute and cwd-relative paths. RegisterProbe in the store validates only the
// prediction pair (the file-existence check lives in the daemon), so culpritFile need
// not exist on disk.
func confirmCulprit(t *testing.T, s *Store, sessionID, culpritFile string) {
	t.Helper()
	if _, err := s.SetHypotheses(sessionID, []string{"suspect one", "suspect two", "suspect three"}); err != nil {
		t.Fatalf("SetHypotheses: %v", err)
	}
	if _, err := s.RegisterProbe(sessionID, Probe{
		ID: "pc", File: culpritFile, Line: 1, HypothesisID: "h1",
		ExpectedIfTrue: "fires", ExpectedIfFalse: "silent",
	}); err != nil {
		t.Fatalf("RegisterProbe: %v", err)
	}
	run, err := s.OpenRun(sessionID)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if _, err := s.CloseRun(sessionID, run.ID, VerdictReproduced); err != nil {
		t.Fatalf("CloseRun: %v", err)
	}
	if _, err := s.UpdateHypothesisWithEvidence(sessionID, "h1", HypothesisConfirmed, "confirmed", "pc", run.ID); err != nil {
		t.Fatalf("confirm: %v", err)
	}
}

// --- False-coverage gate (D-C) ---

// TestSetBlastRadiusNoConfirmedSuspect covers "No confirmed suspect yet": mapping a
// radius while every hypothesis is active (or killed) is rejected.
func TestSetBlastRadiusNoConfirmedSuspect(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	if _, err := s.SetHypotheses(sess.ID, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("SetHypotheses: %v", err)
	}

	_, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern: "foo",
		Hits:    []BlastHit{{File: "src/a.js", Line: 1}},
	})
	if !errors.Is(err, ErrNoConfirmedCulprit) {
		t.Fatalf("want ErrNoConfirmedCulprit with an all-active board, got %v", err)
	}
	// No radius may have been stored.
	got, _ := s.GetSession(sess.ID)
	if got.BlastRadius != nil {
		t.Errorf("rejected radius must not persist: %+v", got.BlastRadius)
	}
}

// TestSetBlastRadiusCulpritMissing covers "Pattern that misses the culprit rejected":
// the confirmed culprit sits in src/auth.js and the search found no hit there, so the
// call fails naming that file and stores nothing.
func TestSetBlastRadiusCulpritMissing(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js")

	_, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern: "readToken",
		Hits: []BlastHit{
			{File: "src/other.js", Line: 4},
			{File: "src/more.js", Line: 9},
		},
	})
	if !errors.Is(err, ErrCulpritNotInRadius) {
		t.Fatalf("want ErrCulpritNotInRadius, got %v", err)
	}
	if !strings.Contains(err.Error(), "src/auth.js") {
		t.Errorf("rejection must name the culprit file src/auth.js: %v", err)
	}
	got, _ := s.GetSession(sess.ID)
	if got.BlastRadius != nil {
		t.Errorf("rejected radius must not persist: %+v", got.BlastRadius)
	}
}

// TestSetBlastRadiusCulpritPresent covers "Pattern covering the culprit accepted":
// the hit set includes the culprit file plus other sites, so the radius is stored and
// returned with all hits, every one unreviewed.
func TestSetBlastRadiusCulpritPresent(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js")

	radius, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern:   "readToken",
		Truncated: false,
		Hits: []BlastHit{
			{File: "src/auth.js", Line: 1, Excerpt: "readToken()"},
			{File: "src/api.js", Line: 12, Excerpt: "readToken()"},
			{File: "src/worker.js", Line: 30, Excerpt: "readToken()"},
			{File: "src/cli.js", Line: 5, Excerpt: "readToken()"},
		},
	})
	if err != nil {
		t.Fatalf("SetBlastRadius accepted case: %v", err)
	}
	if len(radius.Hits) != 4 {
		t.Fatalf("want 4 hits stored, got %d", len(radius.Hits))
	}
	if radius.Unreviewed() != 4 {
		t.Errorf("fresh search must have every hit unreviewed, got %d unreviewed", radius.Unreviewed())
	}
	got, _ := s.GetSession(sess.ID)
	if got.BlastRadius == nil || len(got.BlastRadius.Hits) != 4 {
		t.Fatalf("radius not persisted on session: %+v", got.BlastRadius)
	}
}

// TestSetBlastRadiusPathNormalization proves the culprit-file gate compares paths
// normalized against the session cwd: an absolute culprit probe path still matches a
// cwd-relative hit and vice versa.
func TestSetBlastRadiusPathNormalization(t *testing.T) {
	tests := []struct {
		name        string
		culpritFile string // probe file cited by the confirm
		hitFile     string // the culprit hit's file as recorded by the search
	}{
		{"absolute probe, relative hit", "/repo/src/auth.js", "src/auth.js"},
		{"relative probe, absolute hit", "src/auth.js", "/repo/src/auth.js"},
		{"both relative, dot-prefixed hit", "src/auth.js", "./src/auth.js"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			sess, _, _ := s.CreateSession("", "bug", "/repo")
			confirmCulprit(t, s, sess.ID, tc.culpritFile)

			_, err := s.SetBlastRadius(sess.ID, BlastRadius{
				Pattern: "readToken",
				Hits: []BlastHit{
					{File: tc.hitFile, Line: 1},
					{File: "src/api.js", Line: 12},
				},
			})
			if err != nil {
				t.Fatalf("normalized culprit path should satisfy the gate: %v", err)
			}
		})
	}
}

// --- Replace semantics (D-E) ---

// TestSetBlastRadiusReplaceClearsAnnotations covers "Refined pattern resets review
// state": a re-run swaps the whole radius, and verdicts on the previous search's hits
// never carry over.
func TestSetBlastRadiusReplaceClearsAnnotations(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js")

	if _, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern: "readToken",
		Hits: []BlastHit{
			{File: "src/auth.js", Line: 1},
			{File: "src/api.js", Line: 12},
		},
	}); err != nil {
		t.Fatalf("first SetBlastRadius: %v", err)
	}
	if _, err := s.AnnotateBlastRadius(sess.ID, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSiblingBug, Note: "same bug here"},
	}); err != nil {
		t.Fatalf("AnnotateBlastRadius: %v", err)
	}

	// Re-run with a refined pattern and a different second site.
	replaced, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern: "readToken\\(",
		Hits: []BlastHit{
			{File: "src/auth.js", Line: 1},
			{File: "src/worker.js", Line: 8},
		},
	})
	if err != nil {
		t.Fatalf("second SetBlastRadius: %v", err)
	}
	if replaced.Pattern != "readToken\\(" {
		t.Errorf("radius should reflect the new pattern, got %q", replaced.Pattern)
	}
	if replaced.Unreviewed() != len(replaced.Hits) {
		t.Errorf("every hit of the new search must be unreviewed, got %d/%d", replaced.Unreviewed(), len(replaced.Hits))
	}
	for _, h := range replaced.Hits {
		if h.File == "src/api.js" {
			t.Errorf("old search's site must not carry over: %+v", h)
		}
		if h.Verdict != "" {
			t.Errorf("verdict carried over from a previous search: %+v", h)
		}
	}
}

// --- Annotation merge (D-D) ---

// setupAnnotatable returns a session with a stored radius of four hits (culprit
// src/auth.js plus three siblings), all unreviewed.
func setupAnnotatable(t *testing.T, s *Store) string {
	t.Helper()
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js")
	if _, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern: "readToken",
		Hits: []BlastHit{
			{File: "src/auth.js", Line: 1, Excerpt: "readToken()"},
			{File: "src/api.js", Line: 12, Excerpt: "readToken()"},
			{File: "src/worker.js", Line: 30, Excerpt: "readToken()"},
			{File: "src/cli.js", Line: 5, Excerpt: "readToken()"},
		},
	}); err != nil {
		t.Fatalf("SetBlastRadius: %v", err)
	}
	return sess.ID
}

// TestAnnotateUnknownSiteRejected covers "Annotation of an unrecorded site rejected":
// citing a {file,line} not in the hit set fails naming the unknown site and applies
// no annotation (all-or-nothing).
func TestAnnotateUnknownSiteRejected(t *testing.T) {
	s := newTestStore(t)
	id := setupAnnotatable(t, s)

	_, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSiblingBug}, // valid
		{File: "src/other.js", Line: 10, Verdict: BlastSafe},     // not a recorded hit
	})
	if !errors.Is(err, ErrUnknownRadiusHit) {
		t.Fatalf("want ErrUnknownRadiusHit, got %v", err)
	}
	if !strings.Contains(err.Error(), "src/other.js") {
		t.Errorf("rejection must name the unknown site: %v", err)
	}
	// All-or-nothing: the valid annotation in the same batch must not have applied.
	got, _ := s.GetSession(id)
	if got.BlastRadius.Unreviewed() != 4 {
		t.Errorf("a rejected batch must leave every hit unreviewed, got %d unreviewed", got.BlastRadius.Unreviewed())
	}
}

// TestAnnotateInvalidVerdictRejected covers verdict-enum validation (D-D).
func TestAnnotateInvalidVerdictRejected(t *testing.T) {
	s := newTestStore(t)
	id := setupAnnotatable(t, s)

	_, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastVerdict("garbage")},
	})
	if !errors.Is(err, ErrInvalidBlastVerdict) {
		t.Fatalf("want ErrInvalidBlastVerdict, got %v", err)
	}
	if !strings.Contains(err.Error(), "sibling-bug, safe, already-covered") {
		t.Errorf("error should name the allowed verdicts: %v", err)
	}
	got, _ := s.GetSession(id)
	if got.BlastRadius.Unreviewed() != 4 {
		t.Errorf("a rejected verdict must apply nothing, got %d unreviewed", got.BlastRadius.Unreviewed())
	}
}

// TestAnnotatePartialReviewDisclosed covers "Partial review disclosed": annotating 2
// of 4 hits leaves the radius reporting 2 unreviewed.
func TestAnnotatePartialReviewDisclosed(t *testing.T) {
	s := newTestStore(t)
	id := setupAnnotatable(t, s)

	radius, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSiblingBug, Note: "same defect"},
		{File: "src/worker.js", Line: 30, Verdict: BlastSafe},
	})
	if err != nil {
		t.Fatalf("AnnotateBlastRadius: %v", err)
	}
	if radius.Unreviewed() != 2 {
		t.Errorf("2 of 4 annotated should leave 2 unreviewed, got %d", radius.Unreviewed())
	}
	got, _ := s.GetSession(id)
	if got.BlastRadius.Unreviewed() != 2 {
		t.Errorf("persisted radius should report 2 unreviewed, got %d", got.BlastRadius.Unreviewed())
	}
}

// TestAnnotateOverwriteByFileLine covers overwrite-by-{file,line}: a later verdict on
// the same site wins, both across calls and within a single call.
func TestAnnotateOverwriteByFileLine(t *testing.T) {
	s := newTestStore(t)
	id := setupAnnotatable(t, s)

	// Across calls: first sibling-bug, then safe.
	if _, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSiblingBug, Note: "first"},
	}); err != nil {
		t.Fatalf("first annotate: %v", err)
	}
	if _, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSafe, Note: "second"},
	}); err != nil {
		t.Fatalf("second annotate: %v", err)
	}
	got, _ := s.GetSession(id)
	h := findHitByFile(got.BlastRadius, "src/api.js")
	if h == nil || h.Verdict != BlastSafe || h.Note != "second" {
		t.Errorf("later verdict must win across calls: %+v", h)
	}

	// Within a single call: last duplicate wins.
	radius, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "src/cli.js", Line: 5, Verdict: BlastSiblingBug},
		{File: "src/cli.js", Line: 5, Verdict: BlastAlreadyCovered, Note: "guarded"},
	})
	if err != nil {
		t.Fatalf("dup annotate: %v", err)
	}
	h = findHitByFile(&radius, "src/cli.js")
	if h == nil || h.Verdict != BlastAlreadyCovered || h.Note != "guarded" {
		t.Errorf("last duplicate must win within a call: %+v", h)
	}
}

// TestAnnotateNoRadius covers annotating before any radius exists.
func TestAnnotateNoRadius(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js") // confirmed, but no radius mapped

	_, err := s.AnnotateBlastRadius(sess.ID, []BlastAnnotation{
		{File: "src/auth.js", Line: 1, Verdict: BlastSafe},
	})
	if !errors.Is(err, ErrNoBlastRadius) {
		t.Fatalf("want ErrNoBlastRadius, got %v", err)
	}
}

// TestAnnotateNormalizesPath proves an annotation citing an absolute path still
// matches a cwd-relative recorded hit.
func TestAnnotateNormalizesPath(t *testing.T) {
	s := newTestStore(t)
	id := setupAnnotatable(t, s)

	radius, err := s.AnnotateBlastRadius(id, []BlastAnnotation{
		{File: "/repo/src/api.js", Line: 12, Verdict: BlastSiblingBug},
	})
	if err != nil {
		t.Fatalf("absolute-path annotation should match the relative hit: %v", err)
	}
	h := findHitByFile(&radius, "src/api.js")
	if h == nil || h.Verdict != BlastSiblingBug {
		t.Errorf("normalized annotation did not land on the hit: %+v", h)
	}
}

// --- Persistence (session-state) ---

// TestBlastRadiusRoundTrips covers "Radius round-trips": a radius with four hits (two
// annotated) survives a daemon restart with verdicts and the truncation flag intact.
func TestBlastRadiusRoundTrips(t *testing.T) {
	root := t.TempDir()
	s := newTestStore(t, WithRoot(root))
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	confirmCulprit(t, s, sess.ID, "src/auth.js")

	searchedAt := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	if _, err := s.SetBlastRadius(sess.ID, BlastRadius{
		Pattern:    "readToken\\(",
		Note:       "hunting the missing null-check",
		SearchedAt: searchedAt,
		Truncated:  true,
		Hits: []BlastHit{
			{File: "src/auth.js", Line: 1, Excerpt: "readToken()"},
			{File: "src/api.js", Line: 12, Excerpt: "readToken()"},
			{File: "src/worker.js", Line: 30, Excerpt: "readToken()"},
			{File: "src/cli.js", Line: 5, Excerpt: "readToken()"},
		},
	}); err != nil {
		t.Fatalf("SetBlastRadius: %v", err)
	}
	if _, err := s.AnnotateBlastRadius(sess.ID, []BlastAnnotation{
		{File: "src/api.js", Line: 12, Verdict: BlastSiblingBug, Note: "same defect"},
		{File: "src/worker.js", Line: 30, Verdict: BlastSafe},
	}); err != nil {
		t.Fatalf("AnnotateBlastRadius: %v", err)
	}

	// Restart over the same root.
	s2, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	got, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after restart: %v", err)
	}
	r := got.BlastRadius
	if r == nil {
		t.Fatal("radius did not survive restart")
	}
	if r.Pattern != "readToken\\(" || r.Note != "hunting the missing null-check" {
		t.Errorf("pattern/note not preserved: %+v", r)
	}
	if !r.Truncated {
		t.Error("truncation flag not preserved across restart")
	}
	if !r.SearchedAt.Equal(searchedAt) {
		t.Errorf("searched_at not preserved: got %v want %v", r.SearchedAt, searchedAt)
	}
	if len(r.Hits) != 4 || r.Unreviewed() != 2 {
		t.Fatalf("want 4 hits with 2 unreviewed, got %d hits %d unreviewed", len(r.Hits), r.Unreviewed())
	}
	api := findHitByFile(r, "src/api.js")
	worker := findHitByFile(r, "src/worker.js")
	if api == nil || api.Verdict != BlastSiblingBug || api.Note != "same defect" {
		t.Errorf("annotated sibling-bug hit not preserved: %+v", api)
	}
	if worker == nil || worker.Verdict != BlastSafe {
		t.Errorf("annotated safe hit not preserved: %+v", worker)
	}
	// Excerpts (the search's own facts) survive too.
	if auth := findHitByFile(r, "src/auth.js"); auth == nil || auth.Excerpt != "readToken()" {
		t.Errorf("excerpt not preserved: %+v", auth)
	}
}

// TestLegacySessionLoadsWithoutRadius covers "Legacy session loads without a radius":
// a pre-change state.json (no blast_radius key) resumes with no radius and is not
// rewritten on load.
func TestLegacySessionLoadsWithoutRadius(t *testing.T) {
	root := t.TempDir()
	id := "legacyradius"
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	legacy := `{
  "id": "legacyradius",
  "description": "legacy bug",
  "cwd": "/repo/legacy",
  "created_at": "2024-01-02T03:04:05Z",
  "hypotheses": [
    {"id": "h1", "statement": "suspect one", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"},
    {"id": "h2", "statement": "suspect two", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"},
    {"id": "h3", "statement": "suspect three", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"}
  ],
  "probes": [],
  "runs": []
}`
	statePath := filepath.Join(dir, "state.json")
	if err := os.WriteFile(statePath, []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}
	before, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read legacy state: %v", err)
	}

	s, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("New over legacy root: %v", err)
	}
	got, err := s.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession legacy: %v", err)
	}
	if got.BlastRadius != nil {
		t.Errorf("legacy session should load with no blast radius: %+v", got.BlastRadius)
	}

	// The legacy state file must not have been migrated or rewritten by loading it.
	after, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("re-read legacy state: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Errorf("legacy state was rewritten on load:\nbefore=%s\nafter=%s", before, after)
	}
}

// findHitByFile returns the first hit whose stored (cwd-relative) file equals file,
// or nil. A small test helper so assertions do not depend on hit ordering.
func findHitByFile(r *BlastRadius, file string) *BlastHit {
	for i := range r.Hits {
		if r.Hits[i].File == file {
			return &r.Hits[i]
		}
	}
	return nil
}
