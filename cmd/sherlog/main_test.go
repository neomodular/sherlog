package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/neomodular/sherlog/internal/notes"
)

// TestRenderNotesFilter covers `sherlog notes --category`: only the requested
// category prints, oldest to newest, with the note text shown (field-notes
// Maintainer CLI scenario "Reading the inbox", task 2.3).
func TestRenderNotesFilter(t *testing.T) {
	ns, err := notes.New(notes.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}
	if _, err := ns.Append("s1", "v", notes.CategoryToolBug, "bug one"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := ns.Append("s1", "v", notes.CategoryFriction, "friction one"); err != nil {
		t.Fatalf("Append: %v", err)
	}
	if _, err := ns.Append("s2", "v", notes.CategoryToolBug, "bug two"); err != nil {
		t.Fatalf("Append: %v", err)
	}

	var buf bytes.Buffer
	if err := renderNotes(&buf, ns, notes.CategoryToolBug); err != nil {
		t.Fatalf("renderNotes: %v", err)
	}
	out := buf.String()

	if !strings.Contains(out, "bug one") || !strings.Contains(out, "bug two") {
		t.Errorf("missing tool-bug notes:\n%s", out)
	}
	if strings.Contains(out, "friction one") {
		t.Errorf("friction note leaked into tool-bug filter:\n%s", out)
	}
	// Oldest to newest: "bug one" precedes "bug two".
	if strings.Index(out, "bug one") > strings.Index(out, "bug two") {
		t.Errorf("notes not in chronological order:\n%s", out)
	}
}

// TestRenderNotesEmpty covers the empty-safe path: no notes prints a friendly line
// and no error (absent file yields empty output, not an error).
func TestRenderNotesEmpty(t *testing.T) {
	ns, err := notes.New(notes.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}
	var buf bytes.Buffer
	if err := renderNotes(&buf, ns, ""); err != nil {
		t.Fatalf("renderNotes: %v", err)
	}
	if !strings.Contains(buf.String(), "no field notes") {
		t.Errorf("empty output = %q", buf.String())
	}
}
