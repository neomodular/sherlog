package store

import (
	"strings"
	"testing"
)

// TestTitleRoundTrips covers session-state scenario "Titled session round-trips":
// a session created with a title persists it, survives a daemon restart, and reads
// back with the title and description distinct.
func TestTitleRoundTrips(t *testing.T) {
	root := t.TempDir()
	s := newTestStore(t, WithRoot(root))

	const title = "Login 401 after idle timeout"
	const desc = "Symptom: 401 on the first request after the tab sits idle.\nExpected: silent token refresh keeps the session alive."
	sess, _, err := s.CreateSession(title, desc, "/repo/auth")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Title != title {
		t.Fatalf("create returned title %q, want %q", sess.Title, title)
	}
	if sess.Description != desc {
		t.Errorf("description altered on create: %q", sess.Description)
	}

	// Restart: a fresh store over the same root must recover the stored title.
	s2, err := New(WithRoot(root))
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	got, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after restart: %v", err)
	}
	if got.Title != title {
		t.Errorf("title not persisted across restart: %q want %q", got.Title, title)
	}
	if got.Description != desc {
		t.Errorf("description not persisted across restart: %q", got.Description)
	}
}

// TestLegacyTitleDerivedAtRead covers session-state scenario "Legacy session gets a
// derived title": a session with no stored title (a pre-title state file) reads back
// with a word-boundary-truncated ~60-char title ending in an ellipsis, and the
// stored record is never rewritten with the derived value.
func TestLegacyTitleDerivedAtRead(t *testing.T) {
	s := newTestStore(t)

	// A long description, no title — the legacy shape.
	desc := "the checkout total is off by a single cent on carts that have a percentage discount applied at the basket level, but only sometimes"
	sess, _, err := s.CreateSession("", desc, "/repo/cart")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	got, err := s.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if got.Title == "" {
		t.Fatal("derived title is empty; every payload must carry a non-empty title")
	}
	if !strings.HasSuffix(got.Title, titleEllipsis) {
		t.Errorf("long-description derived title should end in an ellipsis: %q", got.Title)
	}
	// Word-boundary: the derived title must not end mid-word (no trailing partial
	// token before the ellipsis), and its content must stay within the cap.
	content := strings.TrimSuffix(got.Title, titleEllipsis)
	if len(content) > titleMaxLen {
		t.Errorf("derived title content %d chars exceeds cap %d: %q", len(content), titleMaxLen, content)
	}
	if !strings.HasPrefix(desc, content) {
		t.Errorf("derived title %q is not a prefix of the description", content)
	}

	// The stored state file must NOT have been rewritten with the derived title:
	// reading the raw record back shows the title still empty (no migration write).
	raw, err := s.readState(sess.ID)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if raw.Title != "" {
		t.Errorf("stored title was rewritten to %q; legacy files must not be migrated", raw.Title)
	}
}

// TestDeriveTitle guards the fallback derivation contract: short descriptions pass
// through whole, long ones truncate on a word boundary with an ellipsis, newlines
// collapse to one line, and a no-boundary token is hard-cut.
func TestDeriveTitle(t *testing.T) {
	short := "Cart total off by cents"
	if got := deriveTitle(short); got != short {
		t.Errorf("short description should pass through: %q", got)
	}

	multiline := "Symptom: boom\nExpected: no boom"
	if got := deriveTitle(multiline); strings.Contains(got, "\n") {
		t.Errorf("derived title should be a single line: %q", got)
	}

	long := "this description is quite long and runs well past the sixty character cap so it must be truncated"
	got := deriveTitle(long)
	if !strings.HasSuffix(got, titleEllipsis) {
		t.Errorf("long description should be truncated with an ellipsis: %q", got)
	}
	content := strings.TrimSuffix(got, titleEllipsis)
	if len(content) > titleMaxLen {
		t.Errorf("content %d chars exceeds cap %d: %q", len(content), titleMaxLen, content)
	}
	if strings.HasSuffix(content, " ") {
		t.Errorf("word-boundary cut should not leave trailing space: %q", content)
	}

	// A single token longer than the cap has no boundary to cut on: hard-cut.
	noBoundary := strings.Repeat("x", titleMaxLen+20)
	hard := deriveTitle(noBoundary)
	if len(strings.TrimSuffix(hard, titleEllipsis)) != titleMaxLen {
		t.Errorf("no-boundary token should hard-cut at the cap: %q", hard)
	}

	if got := deriveTitle(""); got != "" {
		t.Errorf("empty description should derive an empty title: %q", got)
	}
}

// TestRecallByTitle covers case-recall scenario "Title tokens match": a new
// investigation that shares terms only with a closed case's title still surfaces
// that case, identified by its title with the resolution attached.
func TestRecallByTitle(t *testing.T) {
	s := newTestStore(t)

	// The discriminating term "kerberos" lives only in the title — not in the
	// description, root cause, or hypothesis — so a match proves the title joined
	// the corpus.
	id := closeSolved(t, s, "Kerberos ticket renewal fails", "auth stops working overnight", "/repo/auth",
		&Resolution{RootCause: "expired service ticket", FixSummary: "renew before expiry"})

	matches := s.Recall("kerberos renewal problem")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match via title tokens, got %d: %+v", len(matches), matches)
	}
	m := matches[0]
	if m.SessionID != id {
		t.Errorf("wrong session matched: %q want %q", m.SessionID, id)
	}
	if m.Title != "Kerberos ticket renewal fails" {
		t.Errorf("match should be identified by title: %+v", m)
	}
	if m.RootCause != "expired service ticket" || m.FixSummary != "renew before expiry" {
		t.Errorf("recall match missing resolution: %+v", m)
	}
}

// TestRecallMatchCarriesDerivedTitle verifies a title-less legacy case still
// surfaces in recall results with a non-empty (derived) title, so the skill never
// has to cite a case by a blank identity.
func TestRecallMatchCarriesDerivedTitle(t *testing.T) {
	s := newTestStore(t)
	closeSolved(t, s, "", "intermittent payment webhook signature mismatch on retries", "/repo/pay",
		&Resolution{RootCause: "clock skew invalidates the HMAC window"})

	matches := s.Recall("payment webhook signature mismatch")
	if len(matches) != 1 {
		t.Fatalf("expected 1 match, got %+v", matches)
	}
	if matches[0].Title == "" {
		t.Error("legacy match must carry a non-empty derived title")
	}
}
