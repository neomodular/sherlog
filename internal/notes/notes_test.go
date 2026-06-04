package notes

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return s
}

// TestAppendAndList covers the round trip: appended notes come back in append
// order (oldest first) with every field preserved (field-notes D1).
func TestAppendAndList(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Append("a3f9", "1.2.3", CategoryToolBug, "await returned zero events"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := s.Append("", "1.2.3", CategoryFriction, "no session active"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	list, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	first := list[0]
	if first.Session != "a3f9" || first.Version != "1.2.3" || first.Category != CategoryToolBug {
		t.Errorf("first note = %+v", first)
	}
	if first.Note != "await returned zero events" {
		t.Errorf("first note text = %q", first.Note)
	}
	if first.TS.IsZero() {
		t.Error("timestamp not stamped")
	}
	if list[1].Session != "" {
		t.Errorf("second note session = %q, want empty", list[1].Session)
	}
}

// TestListAbsentFile covers the pure-addition guarantee: no file means empty
// output, not an error (field-notes Maintainer CLI requirement).
func TestListAbsentFile(t *testing.T) {
	s := newTestStore(t)
	list, err := s.List("")
	if err != nil {
		t.Fatalf("List on absent file: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("want empty, got %+v", list)
	}
}

// TestListCategoryFilter covers --category filtering (field-notes D4).
func TestListCategoryFilter(t *testing.T) {
	s := newTestStore(t)
	_, _ = s.Append("s1", "v", CategoryToolBug, "bug one")
	_, _ = s.Append("s1", "v", CategoryFriction, "friction one")
	_, _ = s.Append("s1", "v", CategoryToolBug, "bug two")

	bugs, err := s.List(CategoryToolBug)
	if err != nil {
		t.Fatalf("List(tool-bug): %v", err)
	}
	if len(bugs) != 2 {
		t.Fatalf("tool-bug count = %d, want 2", len(bugs))
	}
	if bugs[0].Note != "bug one" || bugs[1].Note != "bug two" {
		t.Errorf("filtered order wrong: %+v", bugs)
	}
}

// TestAppendInvalidCategory covers the closed enum: an unknown category is
// rejected and nothing is written (field-notes D4).
func TestAppendInvalidCategory(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Append("s1", "v", Category("bogus"), "x"); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("err = %v, want ErrInvalidCategory", err)
	}
	list, _ := s.List("")
	if len(list) != 0 {
		t.Errorf("invalid append leaked a note: %+v", list)
	}
}

// TestListInvalidCategory covers the filter validating its category argument.
func TestListInvalidCategory(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.List(Category("bogus")); !errors.Is(err, ErrInvalidCategory) {
		t.Fatalf("err = %v, want ErrInvalidCategory", err)
	}
}

// TestListTornFinalLine covers tolerance of a torn final line (crash mid-append):
// valid notes still load, the partial line is skipped.
func TestListTornFinalLine(t *testing.T) {
	s := newTestStore(t)
	if _, err := s.Append("s1", "v", CategoryAnomaly, "complete"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	// Simulate a crash mid-append: a partial JSON line with no newline.
	f, err := os.OpenFile(s.path(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_, _ = f.WriteString(`{"ts":"`)
	_ = f.Close()

	list, err := s.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 || list[0].Note != "complete" {
		t.Fatalf("want 1 complete note, got %+v", list)
	}
}

// TestDefaultRootUnderHome covers the default root resolving to ~/.sherlog so all
// local sherlog data lives in one place (field-notes D1).
func TestDefaultRootUnderHome(t *testing.T) {
	s, err := New()
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	home, _ := os.UserHomeDir()
	if want := filepath.Join(home, ".sherlog", fileName); s.path() != want {
		t.Errorf("path = %q, want %q", s.path(), want)
	}
}
