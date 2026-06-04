// Package notes is the field-notes channel (design D2): agent-authored telemetry
// about sherlog itself, appended to a single global JSONL file and read by the
// maintainer. It is deliberately separate from the case-centric store so that
// concern stays out of investigation state (SRP, D2). Notes are local-only and
// never surfaced in user-facing investigation output.
package notes

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

// fileName is the single chronological inbox for all sessions (design D1).
const fileName = "field-notes.jsonl"

// Category is the tiny closed enum that exists solely so `sherlog notes
// --category` can skim (design D4). Resist taxonomy growth.
type Category string

const (
	// CategoryToolBug is a suspected defect in sherlog itself.
	CategoryToolBug Category = "tool-bug"
	// CategoryFriction is awkward or surprising tool ergonomics.
	CategoryFriction Category = "friction"
	// CategoryAnomaly is unexplained sherlog behavior worth a maintainer's eye.
	CategoryAnomaly Category = "anomaly"
	// CategoryOther is anything that does not fit the above.
	CategoryOther Category = "other"
)

// ErrInvalidCategory is returned when a note carries a category outside the enum.
var ErrInvalidCategory = errors.New("invalid note category")

// validCategory reports whether c is one of the closed enum values (D4).
func validCategory(c Category) bool {
	switch c {
	case CategoryToolBug, CategoryFriction, CategoryAnomaly, CategoryOther:
		return true
	default:
		return false
	}
}

// Note is one field-notes record: tool telemetry, not case data (D1). Session is
// the active investigation when one was open at filing time, kept as context
// rather than as an organizing key (a single global file is how a maintainer
// reads them, D1).
type Note struct {
	TS       time.Time `json:"ts"`
	Session  string    `json:"session,omitempty"` // active session when filed, if any
	Version  string    `json:"version"`           // sherlog version that filed it
	Category Category  `json:"category"`
	Note     string    `json:"note"`
}

// Store appends and reads field notes under a root directory (~/.sherlog by
// default, design D1). It reuses the store's atomic-append-line pattern; concurrent
// appends are safe because each is a single O_APPEND write of one line.
type Store struct {
	root string
}

// Option configures a Store.
type Option func(*Store)

// WithRoot overrides the storage root directory (tests inject a temp dir; the
// default is ~/.sherlog, mirroring the investigation store, D1).
func WithRoot(root string) Option {
	return func(s *Store) { s.root = root }
}

// New builds a notes Store. The default root is ~/.sherlog, the same directory
// as the investigation store, so all sherlog local data lives in one place (D1).
func New(opts ...Option) (*Store, error) {
	s := &Store{}
	for _, opt := range opts {
		opt(s)
	}
	if s.root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir for notes root: %w", err)
		}
		s.root = filepath.Join(home, ".sherlog")
	}
	return s, nil
}

// path is the absolute field-notes file path.
func (s *Store) path() string { return filepath.Join(s.root, fileName) }

// Append validates the category and appends one note as a JSON line, stamping it
// with the current UTC time. The root directory is created on demand so a fresh
// install needs no setup. An invalid category is rejected with ErrInvalidCategory
// rather than written, keeping the enum closed (D4).
func (s *Store) Append(session, version string, category Category, note string) (Note, error) {
	if !validCategory(category) {
		return Note{}, fmt.Errorf("%w: %q", ErrInvalidCategory, category)
	}

	n := Note{
		TS:       time.Now().UTC(),
		Session:  session,
		Version:  version,
		Category: category,
		Note:     note,
	}

	if err := os.MkdirAll(s.root, 0o755); err != nil {
		return Note{}, fmt.Errorf("create notes root %q: %w", s.root, err)
	}

	line, err := json.Marshal(n)
	if err != nil {
		return Note{}, fmt.Errorf("marshal note: %w", err)
	}

	f, err := os.OpenFile(s.path(), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return Note{}, fmt.Errorf("open notes file: %w", err)
	}
	defer f.Close()

	if _, err := f.Write(append(line, '\n')); err != nil {
		return Note{}, fmt.Errorf("append note: %w", err)
	}
	return n, nil
}

// List returns all field notes chronologically (file order, newest last — append
// order). A category other than "" filters to that category (D4). An absent notes
// file yields an empty slice, not an error (pure-addition migration: no file = no
// notes). A torn final line (crash mid-append) is skipped rather than failing the
// whole read.
func (s *Store) List(category Category) ([]Note, error) {
	if category != "" && !validCategory(category) {
		return nil, fmt.Errorf("%w: %q", ErrInvalidCategory, category)
	}

	f, err := os.Open(s.path())
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("open notes file: %w", err)
	}
	defer f.Close()

	var out []Note
	sc := bufio.NewScanner(f)
	// Notes may quote investigation context; raise the per-line limit above 64KB.
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var n Note
		if err := json.Unmarshal(line, &n); err != nil {
			continue // tolerate a torn final line
		}
		if category != "" && n.Category != category {
			continue
		}
		out = append(out, n)
	}
	if err := sc.Err(); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("scan notes file: %w", err)
	}
	return out, nil
}
