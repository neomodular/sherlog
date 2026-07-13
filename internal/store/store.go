package store

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ErrSessionNotFound is returned when an operation targets an unknown session ID.
var ErrSessionNotFound = errors.New("session not found")

// ErrNoOpenSession is returned by ResumeLatest when no open session exists.
var ErrNoOpenSession = errors.New("no open session")

// ErrRunNotFound is returned when an operation targets an unknown run ID.
var ErrRunNotFound = errors.New("run not found")

// ErrNoOpenRun is returned by CloseLatestOpenRun when the session has no run
// awaiting a verdict, letting the API answer 409 without a separate lookup.
var ErrNoOpenRun = errors.New("no open run")

// floodKey identifies one flood buffer: events are bounded per probe per run (D8).
type floodKey struct {
	run   string
	probe string
}

// sessionEntry is the in-memory record for one session: its persisted state plus
// the bounded log buffers that are not part of state.json (D5, D8).
type sessionEntry struct {
	session *Session
	floods  map[floodKey]*floodBuffer
	nextRun int // monotonic counter for human-friendly run IDs (r1, r2, ...)

	// appendMu serializes appends to this session's logs.jsonl independently of
	// the global Store.mu. Ingest performs the file write under this per-session
	// lock instead of the global one so a hot-loop probe (D8: 48,201 events) does
	// not block the await engine's polling or other sessions on a file-write
	// syscall (avoids cross-session head-of-line blocking).
	//
	// Acquisition discipline (fix-prerun): writers take appendMu while still
	// holding s.mu, then release s.mu and perform the syscall under appendMu only.
	// This pins on-disk append order to the in-memory record order set under s.mu,
	// so an adopted orphan's event line always lands before its run's adoption
	// marker even when ingest and run-open race.
	appendMu sync.Mutex
}

// recordEvent feeds an event into the right flood buffer, creating it on demand.
// Used both by live ingest and by recovery replay so the bounding logic lives in
// exactly one place (DRY).
func (e *sessionEntry) recordEvent(ev LogEvent, floodN int) {
	key := floodKey{run: ev.Run, probe: ev.Probe}
	buf := e.floods[key]
	if buf == nil {
		buf = newFloodBuffer(floodN)
		e.floods[key] = buf
	}
	buf.add(ev)
}

// hasAdoptableOrphans reports whether any retained orphan event falls in the
// window (from, to], without mutating buffers. openNewRunLocked uses it to decide
// whether a marker is needed and to write that marker BEFORE mutating in-memory
// state, so a marker-write failure leaves memory and disk consistent (no adopted
// memory without a durable marker — fix-prerun D2 atomicity).
func (e *sessionEntry) hasAdoptableOrphans(from, to time.Time) bool {
	for key, buf := range e.floods {
		if key.run != "" {
			continue
		}
		for _, ev := range buf.events() {
			if ev.TS.After(from) && !ev.TS.After(to) {
				return true
			}
		}
	}
	return false
}

// adoptOrphans re-keys orphan events (run "") in the window (from, to] into run,
// splitting each orphan flood buffer by timestamp (design D1/D3). It is the single
// implementation shared by live open and replay (DRY) so attribution is identical
// either way. Returns true when at least one event was adopted.
//
// On a live open the destination floodKey is always absent (a new run owns no
// buffers yet), so the moved buffer is installed directly. Replay is two-pass: its
// first pass may have already loaded direct post-open events under the run's key,
// so when that destination exists the moved orphans are MERGED into it rather than
// overwriting it (design D3) — otherwise replay would discard those direct events
// and undercount the run versus the live view.
func (e *sessionEntry) adoptOrphans(run string, from, to time.Time) bool {
	inWindow := func(ts time.Time) bool { return ts.After(from) && !ts.After(to) }

	adoptedAny := false
	for key, buf := range e.floods {
		if key.run != "" {
			continue
		}
		moved := buf.splitAdopt(run, inWindow)
		if moved == nil {
			continue
		}
		adoptedAny = true

		dstKey := floodKey{run: run, probe: key.probe}
		if dst := e.floods[dstKey]; dst != nil {
			dst.mergeFrom(moved)
		} else {
			e.floods[dstKey] = moved
		}
		// An orphan buffer drained to empty leaves no per-run residue to query.
		if buf.total == 0 {
			delete(e.floods, key)
		}
	}
	return adoptedAny
}

// Store is the in-memory source of truth for all investigations, backed by
// JSON/JSONL files under root (D5). It is safe for concurrent use: HTTP ingest
// handlers and MCP tool calls hit it simultaneously, so every accessor guards
// shared state with mu.
type Store struct {
	root   string
	floodN int

	// resolveCommit pins the repository commit at session creation (D-H). It is a
	// field so tests inject a deterministic resolver and never depend on the test
	// environment being a git repository; the default is the real gitCommit.
	resolveCommit func(cwd string) string

	mu       sync.Mutex
	sessions map[string]*sessionEntry
	counters ingestCounters // activity facts for the health view (add-health-page D2)

	// subMu guards the subscriber set independently of mu (design D3): publish
	// must never run under mu, so event fan-out cannot serialize against ingest or
	// state writes, and a draining subscriber cannot deadlock a publisher.
	subMu sync.Mutex
	subs  map[*subscription]struct{}
}

// Option configures a Store.
type Option func(*Store)

// WithRoot overrides the storage root directory. Tests inject a temp dir; the
// default is ~/.sherlog (D5).
func WithRoot(root string) Option {
	return func(s *Store) { s.root = root }
}

// WithFloodN overrides the first/last-N retained per probe per run (D8, default 20).
func WithFloodN(n int) Option {
	return func(s *Store) {
		if n >= 1 {
			s.floodN = n
		}
	}
}

// WithCommitResolver overrides how a session's commit SHA is resolved at creation
// (D-H). Tests inject a deterministic resolver so the suite never shells out to git
// nor assumes the test environment is a repository; the default is gitCommit.
func WithCommitResolver(fn func(cwd string) string) Option {
	return func(s *Store) {
		if fn != nil {
			s.resolveCommit = fn
		}
	}
}

// New creates a Store and recovers all persisted sessions from disk (D5: state
// survives daemon restart). The default root is ~/.sherlog.
func New(opts ...Option) (*Store, error) {
	s := &Store{floodN: DefaultFloodN, sessions: map[string]*sessionEntry{}, resolveCommit: gitCommit}
	for _, opt := range opts {
		opt(s)
	}
	if s.root == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("resolve home dir for store root: %w", err)
		}
		s.root = home + string(os.PathSeparator) + ".sherlog"
	}

	recovered, err := s.recover()
	if err != nil {
		return nil, fmt.Errorf("recover sessions: %w", err)
	}
	s.sessions = recovered
	// Restore each session's run counter so newly opened runs do not collide
	// with IDs already on disk.
	for _, entry := range s.sessions {
		entry.nextRun = maxRunNumber(entry.session.Runs)
	}
	return s, nil
}

// Root reports the resolved storage root (used by the CLI to scan all sessions).
func (s *Store) Root() string { return s.root }

// --- Session lifecycle (spec: Session lifecycle) ---

// CreateSession opens a new investigation for the given title, bug description,
// and cwd. The ID is a fresh random base36 token (D4). The title is the agent-
// authored case identity (add-case-titles D1); an empty title is left empty in
// storage and derived from the description at read time (effectiveTitle), so an
// older caller that omits it still yields a non-empty title in every payload
// without a migration write. If another open session already exists for the same
// cwd it is returned so the caller can warn the user; the new session is still
// created (concurrent sessions are allowed, D-risks).
func (s *Store) CreateSession(title, description, cwd string) (created *Session, existingSameCWD *Session, err error) {
	// Resolve the commit BEFORE taking the lock: the git invocation can take up to
	// its timeout, and holding s.mu across it would stall every other store
	// operation (ingest, the await poll loop) for that whole window (D-H).
	commit := s.resolveCommit(cwd)

	s.mu.Lock()
	defer s.mu.Unlock()

	if existing := s.findOpenByCWDLocked(cwd); existing != nil {
		existingSameCWD = cloneSession(existing.session)
	}

	id, err := s.uniqueIDLocked()
	if err != nil {
		return nil, nil, err
	}

	sess := &Session{
		ID:          id,
		Title:       strings.TrimSpace(title),
		Description: description,
		CWD:         cwd,
		Commit:      commit, // best-effort pinned commit (D-H); empty when unavailable
		CreatedAt:   time.Now().UTC(),
		Hypotheses:  []Hypothesis{},
		Probes:      []Probe{},
		Runs:        []Run{},
	}
	entry := &sessionEntry{session: sess, floods: map[floodKey]*floodBuffer{}}
	s.sessions[id] = entry

	if err := s.writeState(sess); err != nil {
		delete(s.sessions, id)
		return nil, nil, err
	}
	return cloneSession(sess), existingSameCWD, nil
}

// CloseSession transitions a session to closed-unsolved and reports every probe
// still awaiting cleanup, i.e. with Removed unset (D10). Closing an already-closed
// session is idempotent. Use CloseSessionWithResolution to record a root cause.
func (s *Store) CloseSession(id string) (unremoved []Probe, err error) {
	return s.CloseSessionWithResolution(id, nil)
}

// CloseSessionWithResolution transitions a session to closed, optionally recording
// the resolution that feeds the closed-case view and recall (D4). A nil or empty
// resolution closes the case unsolved (Resolution stays nil), so a case is never
// recorded as solved with nothing to show. The resolution's ClosedAt is stamped
// here so it always matches the session close time. Closing an already-closed
// session is idempotent and leaves any existing resolution untouched. Returns
// every probe still awaiting cleanup (Removed unset, D10).
func (s *Store) CloseSessionWithResolution(id string, res *Resolution) (unremoved []Probe, err error) {
	s.mu.Lock()

	entry, ok := s.sessions[id]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("close session %q: %w", id, ErrSessionNotFound)
	}

	newlyClosed := false
	if entry.session.ClosedAt == nil {
		// Validate a solved close BEFORE any mutation (D-F): a rejection returns an
		// error and leaves the session OPEN — never a silent downgrade to unsolved. A
		// nil/empty resolution is an unsolved close and skips validation entirely.
		// Validation runs only on the actual open→closed transition; an already-closed
		// session is idempotent (below) and ignores the supplied resolution, so there
		// is nothing to validate on a re-close.
		if res != nil && !res.IsEmpty() {
			if err := validateResolutionLocked(entry.session, res); err != nil {
				s.mu.Unlock()
				return nil, fmt.Errorf("close session %q: %w", id, err)
			}
		}

		now := time.Now().UTC()
		entry.session.ClosedAt = &now
		// A non-empty resolution has already passed validation above; an all-empty
		// resolution is an unsolved close, keeping Resolution nil so recall never
		// matches a case that recorded no root cause (session-state spec).
		if res != nil && !res.IsEmpty() {
			r := *res
			r.ClosedAt = now
			entry.session.Resolution = &r
		}
		if err := s.writeState(entry.session); err != nil {
			s.mu.Unlock()
			return nil, err
		}
		newlyClosed = true
	}

	for _, p := range entry.session.Probes {
		if !p.Removed {
			unremoved = append(unremoved, p)
		}
	}
	closed := cloneSession(entry.session)
	s.mu.Unlock()

	// Publish off-lock (design D3): the run channel signals the case left the open
	// set so a Case Board can refresh. Only emit on the transition, not on the
	// idempotent re-close.
	if newlyClosed {
		s.publish(Event{Kind: EventRun, Session: id, Payload: closed})
	}
	return unremoved, nil
}

// GetSession returns a copy of a session's full state, or ErrSessionNotFound.
func (s *Store) GetSession(id string) (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[id]
	if !ok {
		return nil, fmt.Errorf("get session %q: %w", id, ErrSessionNotFound)
	}
	return cloneSession(entry.session), nil
}

// ListSessions returns a copy of every session for the Case Board's case list,
// ordered open-first then closed, each group most-recent-first (case-board-ui
// spec: open first, then closed). Open sessions sort by creation time; closed
// ones by close time so the freshly solved case leads the archive. Copies are
// defensive — callers never receive pointers into live store state.
func (s *Store) ListSessions() []*Session {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*Session, 0, len(s.sessions))
	for _, entry := range s.sessions {
		out = append(out, cloneSession(entry.session))
	}
	sort.Slice(out, func(i, j int) bool {
		oi, oj := out[i].ClosedAt == nil, out[j].ClosedAt == nil
		if oi != oj {
			return oi // open sessions first
		}
		if oi { // both open: newest created first
			return out[i].CreatedAt.After(out[j].CreatedAt)
		}
		// Both closed: newest close first so the latest solved case leads.
		return out[i].ClosedAt.After(*out[j].ClosedAt)
	})
	return out
}

// ResumeLatest returns the most recently created open session for debug_resume
// with no argument (spec: Investigation resume). ErrNoOpenSession when none.
func (s *Store) ResumeLatest() (*Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var latest *Session
	for _, entry := range s.sessions {
		sess := entry.session
		if sess.ClosedAt != nil {
			continue
		}
		if latest == nil || sess.CreatedAt.After(latest.CreatedAt) {
			latest = sess
		}
	}
	if latest == nil {
		return nil, ErrNoOpenSession
	}
	return cloneSession(latest), nil
}

// --- Hypothesis board (spec: Hypothesis board persisted in daemon) ---

// SetHypotheses replaces the session's board with the given statements, assigning
// sequential IDs (h1, h2, ...) and status active. This backs set_hypotheses (D9).
// A board of fewer than three suspects is rejected (D-E) and the existing board is
// left unchanged — replace semantics are preserved, so a mid-investigation split
// must still resubmit the full board of at least three.
func (s *Store) SetHypotheses(sessionID string, statements []string) ([]Hypothesis, error) {
	if len(statements) < minHypotheses {
		return nil, fmt.Errorf("set hypotheses for %q: %w — got %d, name at least three distinct suspects", sessionID, ErrInsufficientHypotheses, len(statements))
	}

	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return nil, fmt.Errorf("set hypotheses for %q: %w", sessionID, ErrSessionNotFound)
	}

	now := time.Now().UTC()
	board := make([]Hypothesis, 0, len(statements))
	for i, stmt := range statements {
		board = append(board, Hypothesis{
			ID:        "h" + strconv.Itoa(i+1),
			Statement: stmt,
			Status:    HypothesisActive,
			CreatedAt: now,
			UpdatedAt: now,
		})
	}
	entry.session.Hypotheses = board

	if err := s.writeState(entry.session); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	out := cloneHypotheses(board)
	s.mu.Unlock()

	s.publish(Event{Kind: EventBoard, Session: sessionID, Payload: out})
	return out, nil
}

// UpdateHypothesis sets a hypothesis's status and evidence note (D6). It is the
// no-citation form: a transition to active (refine) carries no citation and is
// accepted, while a kill or confirm is rejected by the evidence-citation gate
// (D-B) because it supplies no citation. Callers that kill or confirm must use
// UpdateHypothesisWithEvidence.
func (s *Store) UpdateHypothesis(sessionID, hypothesisID string, status HypothesisStatus, note string) (Hypothesis, error) {
	return s.UpdateHypothesisWithEvidence(sessionID, hypothesisID, status, note, "", "")
}

// UpdateHypothesisWithEvidence sets a hypothesis's status, note, and — for a kill
// or confirm — the evidence citation the store cross-checks before accepting the
// transition (harden-detective-gates D-B, D-C). A transition to active requires no
// citation. For killed/confirmed the store validates that the cited probe is
// registered and the cited run exists and is closed with a verdict; a confirm
// additionally requires at least one reproduced run in the session and a cited
// probe carrying a prediction pair. On any gate failure the board is left
// unchanged (fail before mutating). The accepted citation persists on the
// hypothesis alongside the free-text note.
func (s *Store) UpdateHypothesisWithEvidence(sessionID, hypothesisID string, status HypothesisStatus, note, evidenceProbeID, evidenceRunID string) (Hypothesis, error) {
	// Pure input validation, like SetHypotheses' board floor: an unknown status must
	// never reach the board — it would sidestep the kill/confirm citation gates below.
	if err := validateHypothesisStatus(status); err != nil {
		return Hypothesis{}, fmt.Errorf("update hypothesis %q in %q: %w", hypothesisID, sessionID, err)
	}

	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Hypothesis{}, fmt.Errorf("update hypothesis in %q: %w", sessionID, ErrSessionNotFound)
	}

	// Resolve the hypothesis before validating the citation so an unknown hypothesis
	// or session reports its own error rather than a citation error (stable error
	// precedence: session → hypothesis → citation).
	h := findHypothesis(entry.session, hypothesisID)
	if h == nil {
		s.mu.Unlock()
		return Hypothesis{}, fmt.Errorf("update hypothesis %q in %q: %w", hypothesisID, sessionID, ErrHypothesisNotFound)
	}

	cite := status == HypothesisKilled || status == HypothesisConfirmed
	if cite {
		if err := validateCitationLocked(entry.session, status, evidenceProbeID, evidenceRunID); err != nil {
			s.mu.Unlock()
			return Hypothesis{}, fmt.Errorf("update hypothesis %q in %q: %w", hypothesisID, sessionID, err)
		}
	}

	h.Status = status
	if note != "" {
		h.Note = note
	}
	if cite {
		// Persist the validated citation alongside the note (D-B).
		h.EvidenceProbeID = evidenceProbeID
		h.EvidenceRunID = evidenceRunID
	}
	h.UpdatedAt = time.Now().UTC()
	if err := s.writeState(entry.session); err != nil {
		s.mu.Unlock()
		return Hypothesis{}, err
	}
	updated := *h
	s.mu.Unlock()

	s.publish(Event{Kind: EventBoard, Session: sessionID, Payload: updated})
	return updated, nil
}

// ErrHypothesisNotFound is returned when a hypothesis ID is unknown in a session.
var ErrHypothesisNotFound = errors.New("hypothesis not found")

// --- Probe registry (spec: Probe registry) ---

// RegisterProbe records a placed probe so cleanup is guaranteed findable (D10).
// Re-registering an existing probe ID updates it in place. The optional prediction
// pair is validated (D-A: both-or-neither, and non-equal when present) before the
// probe is stored; the file/line existence check lives in the daemon, which alone
// reliably knows the session cwd (D-G).
func (s *Store) RegisterProbe(sessionID string, p Probe) (Probe, error) {
	if strings.TrimSpace(p.ID) == "" {
		return Probe{}, fmt.Errorf("register probe in %q: a probe needs a non-empty id (p1, p2, …): %w", sessionID, ErrProbeIDRequired)
	}
	if err := validatePredictionPair(p.ExpectedIfTrue, p.ExpectedIfFalse); err != nil {
		return Probe{}, fmt.Errorf("register probe in %q: %w", sessionID, err)
	}

	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Probe{}, fmt.Errorf("register probe in %q: %w", sessionID, ErrSessionNotFound)
	}

	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	replaced := false
	for i := range entry.session.Probes {
		if entry.session.Probes[i].ID == p.ID {
			entry.session.Probes[i] = p
			replaced = true
			break
		}
	}
	if !replaced {
		entry.session.Probes = append(entry.session.Probes, p)
	}
	if err := s.writeState(entry.session); err != nil {
		s.mu.Unlock()
		return Probe{}, err
	}
	s.mu.Unlock()

	s.publish(Event{Kind: EventProbe, Session: sessionID, Payload: p})
	return p, nil
}

// RemoveProbe marks a probe removed once its line is deleted from code (D10).
func (s *Store) RemoveProbe(sessionID, probeID string) error {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("remove probe in %q: %w", sessionID, ErrSessionNotFound)
	}

	for i := range entry.session.Probes {
		if entry.session.Probes[i].ID == probeID {
			entry.session.Probes[i].Removed = true
			if err := s.writeState(entry.session); err != nil {
				s.mu.Unlock()
				return err
			}
			removed := entry.session.Probes[i]
			s.mu.Unlock()

			s.publish(Event{Kind: EventProbe, Session: sessionID, Payload: removed})
			return nil
		}
	}
	s.mu.Unlock()
	return fmt.Errorf("remove probe %q in %q: %w", probeID, sessionID, ErrProbeNotFound)
}

// ErrProbeNotFound is returned when a probe ID is unknown in a session.
var ErrProbeNotFound = errors.New("probe not found")

// StaleProbe is an unremoved probe across any session, for `sherlog probes --stale` (D10).
// SessionTitle carries the owning case's title so the Case Board's stale-probes
// rows identify the case by name, not bare ID (add-case-titles: case references
// show the title). Always non-empty — a title-less legacy case carries its derived
// fallback.
type StaleProbe struct {
	SessionID    string `json:"session_id"`
	SessionTitle string `json:"session_title"`
	Probe        Probe  `json:"probe"`
}

// StaleProbes lists every registered-but-not-removed probe across all sessions,
// the "weeks later" safety net for orphaned probes (D10). Results are sorted by
// session ID then probe ID for stable output.
func (s *Store) StaleProbes() []StaleProbe {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []StaleProbe
	for id, entry := range s.sessions {
		title := effectiveTitle(entry.session)
		for _, p := range entry.session.Probes {
			if !p.Removed {
				out = append(out, StaleProbe{SessionID: id, SessionTitle: title, Probe: p})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].SessionID != out[j].SessionID {
			return out[i].SessionID < out[j].SessionID
		}
		return out[i].Probe.ID < out[j].Probe.ID
	})
	return out
}

// --- Runs (D7) ---

// OpenRun starts a new run on a session and returns its ID. await_run opens a run
// before the user reproduces the bug; every log event is stamped with it (D7).
// Opening a new run adopts eligible pre-run orphan events into it (fix-prerun D1).
func (s *Store) OpenRun(sessionID string) (Run, error) {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Run{}, fmt.Errorf("open run in %q: %w", sessionID, ErrSessionNotFound)
	}
	run, err := s.openNewRunLocked(entry, "")
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}

	s.publish(Event{Kind: EventRun, Session: sessionID, Payload: run})
	return run, nil
}

// OpenOrAttachRun returns the session's latest open run, or opens a new one if
// none is open — atomically, under a single critical section. await uses this so
// concurrent calls on a session with no open run converge on one run instead of
// each opening its own (the LatestOpenRun+OpenRun pair was TOCTOU-racy, D8). This
// is the no-prediction form; use OpenOrAttachRunWithPrediction to stamp a fix
// prediction (D-D).
func (s *Store) OpenOrAttachRun(sessionID string) (Run, error) {
	return s.OpenOrAttachRunWithPrediction(sessionID, "")
}

// OpenOrAttachRunWithPrediction opens (or atomically re-attaches to) the session's
// latest run and stamps an optional fix prediction on it (harden-detective-gates
// D-D). The prediction is recorded at call receipt — before any evidence summary
// is returned — and only when the run does not already carry one: it is immutable
// once set, so supplying a prediction on a re-attach whose run has none is
// accepted, while overwriting an existing one is silently ignored. A fixed-check
// close later requires this prediction to be present (CloseRun / CloseLatestOpenRun).
func (s *Store) OpenOrAttachRunWithPrediction(sessionID, prediction string) (Run, error) {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Run{}, fmt.Errorf("open-or-attach run in %q: %w", sessionID, ErrSessionNotFound)
	}

	for i := len(entry.session.Runs) - 1; i >= 0; i-- {
		if entry.session.Runs[i].ClosedAt == nil {
			// Re-attach to the open run: adoption happens only at open, never on
			// re-attach (fix-prerun D1), so this path must not adopt.
			r := &entry.session.Runs[i]
			// Stamp the prediction only if the run has none yet (immutable once set,
			// D-D). Persist + publish only when a stamp actually happened; otherwise
			// nothing changed, so no run event is published.
			if prediction != "" && r.Prediction == "" {
				now := time.Now().UTC()
				r.Prediction = prediction
				r.PredictionAt = &now
				if err := s.writeState(entry.session); err != nil {
					// Roll back the stamp so a failed persist leaves no phantom prediction.
					r.Prediction = ""
					r.PredictionAt = nil
					s.mu.Unlock()
					return Run{}, err
				}
				run := *r
				s.mu.Unlock()
				s.publish(Event{Kind: EventRun, Session: sessionID, Payload: run})
				return run, nil
			}
			run := *r
			s.mu.Unlock()
			return run, nil
		}
	}
	run, err := s.openNewRunLocked(entry, prediction)
	s.mu.Unlock()
	if err != nil {
		return Run{}, err
	}

	s.publish(Event{Kind: EventRun, Session: sessionID, Payload: run})
	return run, nil
}

// openNewRunLocked opens a fresh run, persists an append-only adoption marker for
// eligible pre-run orphans, then adopts them in memory (fix-prerun D1/D2). The
// marker is written before the in-memory mutation so a marker-write failure can
// abort the open with memory and disk still in agreement. It is the single
// new-run path shared by OpenRun and OpenOrAttachRun so adoption can never be
// bypassed by one entry point. A non-empty prediction is stamped on the run at
// creation with a PredictionAt timestamp (D-D). Callers hold s.mu.
func (s *Store) openNewRunLocked(entry *sessionEntry, prediction string) (Run, error) {
	now := time.Now().UTC()
	entry.nextRun++
	run := Run{ID: "r" + strconv.Itoa(entry.nextRun), StartedAt: now}
	if prediction != "" {
		run.Prediction = prediction
		predAt := now
		run.PredictionAt = &predAt
	}
	entry.session.Runs = append(entry.session.Runs, run)
	if err := s.writeState(entry.session); err != nil {
		// Roll back the in-memory run so a failed persist does not leave a
		// phantom run that never reached disk.
		entry.session.Runs = entry.session.Runs[:len(entry.session.Runs)-1]
		entry.nextRun--
		return Run{}, err
	}

	// Adopt orphans whose timestamps fall after the last run boundary and within
	// the 15-minute cap (D1). The boundary is the lower of the two so anything
	// after the prior verdict, but not ancient, joins this run.
	//
	// Persist the marker BEFORE mutating in-memory buffers so memory and disk
	// cannot disagree: a marker-write failure aborts open with no adoption
	// applied, rather than leaving adopted-in-memory state that a restart would
	// revert to orphans (fix-prerun D2 atomicity).
	from := adoptionLowerBound(entry.session, run.ID, now)
	if entry.hasAdoptableOrphans(from, now) {
		if err := s.appendAdoptMarker(entry, adoptMarker{Run: run.ID, From: from, To: now}); err != nil {
			// Marker write failed before any in-memory adoption: roll back the run so
			// the aborted open leaves memory and disk in agreement (D2 atomicity).
			entry.session.Runs = entry.session.Runs[:len(entry.session.Runs)-1]
			// Re-persist; if that also fails, disk still holds the phantom run, so
			// join the errors to disclose the inconsistency rather than hide it.
			if rbErr := s.writeState(entry.session); rbErr != nil {
				return Run{}, errors.Join(err, fmt.Errorf("roll back run state after marker failure: %w", rbErr))
			}
			return Run{}, err
		}
		entry.adoptOrphans(run.ID, from, now)
	}
	return run, nil
}

// adoptionCap bounds how far back a newly opened run reaches for orphans, so an
// hour-old straggler never pollutes a fresh attempt (design D1).
const adoptionCap = 15 * time.Minute

// adoptionLowerBound is the exclusive start of the adoption window for newRunID:
// the latest *previous* run's close time, else the session start, raised to the
// 15-minute cap (design D1). Only post-boundary orphans can belong to this run,
// because any verdict was already given on what was visible before it.
func adoptionLowerBound(sess *Session, newRunID string, now time.Time) time.Time {
	boundary := sess.CreatedAt // session start when no prior run closed
	for _, r := range sess.Runs {
		if r.ID == newRunID || r.ClosedAt == nil {
			continue
		}
		if r.ClosedAt.After(boundary) {
			boundary = *r.ClosedAt
		}
	}
	if cap := now.Add(-adoptionCap); cap.After(boundary) {
		boundary = cap
	}
	return boundary
}

// CloseRun records the user's verdict on a run (D7).
func (s *Store) CloseRun(sessionID, runID string, verdict RunVerdict) (Run, error) {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Run{}, fmt.Errorf("close run in %q: %w", sessionID, ErrSessionNotFound)
	}

	for i := range entry.session.Runs {
		r := &entry.session.Runs[i]
		if r.ID != runID {
			continue
		}
		// A fixed-check verdict requires a fix prediction recorded before the evidence
		// was returned (D-D): reject before mutating so the run stays open for a
		// re-await that supplies the prediction.
		if verdict == VerdictFixedCheck && r.Prediction == "" {
			s.mu.Unlock()
			return Run{}, fmt.Errorf("close run %q in %q: %w — re-await with a prediction and have the user reproduce once more", runID, sessionID, ErrFixedCheckNeedsPrediction)
		}
		if r.ClosedAt == nil {
			now := time.Now().UTC()
			r.ClosedAt = &now
		}
		r.Verdict = verdict
		if err := s.writeState(entry.session); err != nil {
			s.mu.Unlock()
			return Run{}, err
		}
		closed := *r
		s.mu.Unlock()

		s.publish(Event{Kind: EventRun, Session: sessionID, Payload: closed})
		return closed, nil
	}
	s.mu.Unlock()
	return Run{}, fmt.Errorf("close run %q in %q: %w", runID, sessionID, ErrRunNotFound)
}

// LatestOpenRun returns the most recently opened run with no verdict, or false.
// await_run re-attaches to it so re-invocation is idempotent (D8).
func (s *Store) LatestOpenRun(sessionID string) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return Run{}, false, fmt.Errorf("latest open run in %q: %w", sessionID, ErrSessionNotFound)
	}
	for i := len(entry.session.Runs) - 1; i >= 0; i-- {
		if entry.session.Runs[i].ClosedAt == nil {
			return entry.session.Runs[i], true, nil
		}
	}
	return Run{}, false, nil
}

// CloseLatestOpenRun records a verdict on the session's latest open run as a
// single atomic operation, mirroring OpenOrAttachRun's discipline (D7). The
// find-then-close pair across two lock acquisitions was TOCTOU-racy: a
// concurrent close could leave the second caller acting on a stale run. Returns
// ErrNoOpenRun when no run awaits a verdict so the API can answer 409.
func (s *Store) CloseLatestOpenRun(sessionID string, verdict RunVerdict) (Run, error) {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return Run{}, fmt.Errorf("close latest open run in %q: %w", sessionID, ErrSessionNotFound)
	}

	for i := len(entry.session.Runs) - 1; i >= 0; i-- {
		r := &entry.session.Runs[i]
		if r.ClosedAt != nil {
			continue
		}
		// A fixed-check verdict requires a prediction recorded before evidence
		// returned (D-D): reject before mutating so the run stays open for a re-await.
		if verdict == VerdictFixedCheck && r.Prediction == "" {
			runID := r.ID
			s.mu.Unlock()
			return Run{}, fmt.Errorf("close latest open run %q in %q: %w — re-await with a prediction and have the user reproduce once more", runID, sessionID, ErrFixedCheckNeedsPrediction)
		}
		now := time.Now().UTC()
		r.ClosedAt = &now
		r.Verdict = verdict
		if err := s.writeState(entry.session); err != nil {
			s.mu.Unlock()
			return Run{}, err
		}
		closed := *r
		s.mu.Unlock()

		s.publish(Event{Kind: EventRun, Session: sessionID, Payload: closed})
		return closed, nil
	}
	s.mu.Unlock()
	return Run{}, fmt.Errorf("close latest open run in %q: %w", sessionID, ErrNoOpenRun)
}

// --- Ingest & query ---

// Ingest records a probe hit. The owning run, when empty, defaults to the
// session's latest open run so events fired during await_run are attributed
// correctly (D7). Unknown sessions are rejected with ErrSessionNotFound so the
// HTTP handler can drop drive-by POSTs silently (D4).
func (s *Store) Ingest(sessionID, probeID string, body any, raw string) error {
	// In-memory mutation under the global lock is fast and bounded (D8); the
	// disk append syscall is performed after the global lock is released so it
	// cannot serialize the await engine or other sessions. The per-session
	// appendMu is taken before that release to fix on-disk ordering (see below).
	s.mu.Lock()
	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return fmt.Errorf("ingest for %q: %w", sessionID, ErrSessionNotFound)
	}

	run := ""
	for i := len(entry.session.Runs) - 1; i >= 0; i-- {
		if entry.session.Runs[i].ClosedAt == nil {
			run = entry.session.Runs[i].ID
			break
		}
	}

	ev := LogEvent{TS: time.Now().UTC(), Run: run, Probe: probeID, Body: body, Raw: raw}
	entry.recordEvent(ev, s.floodN)
	// Activity counters are mutated here, under s.mu, so the health view's totals and
	// hourly window stay consistent with the in-memory event record (add-health-page
	// D2). They count every ingested event regardless of flood-control retention.
	s.counters.recordIngest(ev.TS)

	// Two-pass replay (replayLogs loads all events before applying any marker) is
	// the primary guarantee that an adopted orphan survives restart regardless of
	// on-disk interleaving. Acquiring appendMu BEFORE releasing s.mu is
	// defense-in-depth: it pins on-disk append order to in-memory record order, so
	// an adopted orphan's line still precedes its run's marker on disk. The syscall
	// runs after s.mu is released, so the global lock never spans a write.
	entry.appendMu.Lock()
	s.mu.Unlock()
	if err := s.appendLog(sessionID, ev); err != nil {
		entry.appendMu.Unlock()
		return err
	}
	entry.appendMu.Unlock()

	// Publish after the durable append so a subscriber never sees a log event that
	// failed to persist (design D3). publish takes subMu, not s.mu/appendMu, so the
	// hot ingest path is never serialized by event fan-out.
	s.publish(Event{Kind: EventLog, Session: sessionID, Payload: ev})
	return nil
}

// ProbeSummary is the per-probe view of a run: the true total plus the retained
// first/last-N events and whether the middle was dropped (D8). Adopted reports how
// many of Total were attributed by pre-run adoption rather than directly, so the
// caller can always tell inferred attribution from direct (fix-prerun design D4).
type ProbeSummary struct {
	Probe     string     `json:"probe"`
	Run       string     `json:"run"`
	Total     int        `json:"total"`
	Adopted   int        `json:"adopted"`
	Truncated bool       `json:"truncated"`
	Events    []LogEvent `json:"events"`
}

// RunTotal sums the true event count across every probe in a run without
// materializing per-probe summaries. The await engine polls this ~10x/sec (D8)
// as its activity signal, so it must stay cheap even under a hot-loop probe
// (48,201 events) — it reads only counters and never allocates retained events.
func (s *Store) RunTotal(sessionID, runID string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return 0, fmt.Errorf("run total for %q: %w", sessionID, ErrSessionNotFound)
	}

	total := 0
	for key, buf := range entry.floods {
		if key.run == runID {
			total += buf.total
		}
	}
	return total, nil
}

// RunSummary returns one ProbeSummary per probe that fired in the run, sorted by
// probe ID. This is the shape await_run reports to the skill (D8): counts and
// first/last samples, never raw dumps.
func (s *Store) RunSummary(sessionID, runID string) ([]ProbeSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("run summary for %q: %w", sessionID, ErrSessionNotFound)
	}

	var out []ProbeSummary
	for key, buf := range entry.floods {
		if key.run != runID {
			continue
		}
		out = append(out, ProbeSummary{
			Probe:     key.probe,
			Run:       key.run,
			Total:     buf.total,
			Adopted:   buf.adopted,
			Truncated: buf.truncated(),
			Events:    buf.events(),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Probe < out[j].Probe })
	return out, nil
}

// QueryFilter narrows query_logs results (D9). A zero-value field means "any".
type QueryFilter struct {
	Run   string // limit to one run
	Probe string // limit to one probe
	Limit int    // cap returned events (0 = no cap)
}

// QueryResult is one matching (run, probe) bucket with its true total and the
// retained events, capped by the filter limit (D8: truncation always disclosed).
// Adopted carries pre-run adoption disclosure through query results, mirroring
// ProbeSummary (fix-prerun design D4).
type QueryResult struct {
	Probe     string     `json:"probe"`
	Run       string     `json:"run"`
	Total     int        `json:"total"`
	Adopted   int        `json:"adopted"`
	Truncated bool       `json:"truncated"`
	Events    []LogEvent `json:"events"`
}

// QueryLogs returns retained events matching the filter, grouped by (run, probe)
// and sorted by run then probe. Total reflects the true ingested count even when
// events were dropped by flood control or the filter limit (D8, D9).
func (s *Store) QueryLogs(sessionID string, f QueryFilter) ([]QueryResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		return nil, fmt.Errorf("query logs for %q: %w", sessionID, ErrSessionNotFound)
	}

	var out []QueryResult
	for key, buf := range entry.floods {
		if f.Run != "" && key.run != f.Run {
			continue
		}
		if f.Probe != "" && key.probe != f.Probe {
			continue
		}
		events := buf.events()
		truncated := buf.truncated()
		if f.Limit > 0 && len(events) > f.Limit {
			events = events[:f.Limit]
			truncated = true
		}
		out = append(out, QueryResult{
			Probe:     key.probe,
			Run:       key.run,
			Total:     buf.total,
			Adopted:   buf.adopted,
			Truncated: truncated,
			Events:    events,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Run != out[j].Run {
			return out[i].Run < out[j].Run
		}
		return out[i].Probe < out[j].Probe
	})
	return out, nil
}

// --- Retention pruning (configuration spec: Retention pruning) ---

// PruneClosedBefore deletes every session closed strictly before cutoff from both
// memory and disk, returning the IDs removed (sorted) so the caller can log them.
// Open sessions are never pruned regardless of age — this is the only deletion path
// in sherlog and it must never touch a live investigation. A session whose on-disk
// directory cannot be removed is left in memory and excluded from the result so a
// failed delete is not falsely reported as pruned.
//
// The whole prune — eligibility check, os.RemoveAll, and map delete — runs under a
// single s.mu critical section. This serializes prune against the writeState-based
// reopen paths (CreateSession/OpenRun/OpenOrAttachRun), all of which mutate the map
// and write state on-lock: while s.mu is held none of them can run, so none can
// reopen a session that is about to be forgotten from memory nor re-create its dir
// behind os.RemoveAll. It does NOT serialize against Ingest's disk append: Ingest
// re-MkdirAlls the dir inside appendLog, which runs off s.mu (under appendMu only)
// after the global lock is released. prune never takes appendMu, so a drive-by POST
// ingesting into an already-closed session can race os.RemoveAll and resurrect a
// zombie on-disk directory. This residual race is accepted: it requires an ingest to
// a session that was closed strictly before the cutoff (> retention_days ago), the
// worst case is an orphan directory for a non-live session (open sessions stay
// immune, the in-memory entry is already gone, and replay would re-adopt nothing of
// value), and closing it would mean holding every session's appendMu across the
// syscall — a far heavier coupling than the leak it prevents. Holding s.mu across
// os.RemoveAll departs from ingest's filesystem-off-the-lock discipline (see
// appendMu) but is bounded: prune touches only already-closed sessions, runs on a
// slow timer rather than the hot ingest path, and removing already-closed dirs
// cannot contend with the await engine's polling of open runs.
func (s *Store) PruneClosedBefore(cutoff time.Time) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var pruned []string
	for id, entry := range s.sessions {
		closed := entry.session.ClosedAt
		if closed == nil || !closed.Before(cutoff) {
			continue // open, or closed recently enough to keep
		}
		if err := os.RemoveAll(s.sessionDir(id)); err != nil {
			// Leave it in memory; a transient FS error must not drop the session
			// silently nor be reported as a successful prune.
			sort.Strings(pruned)
			return pruned, fmt.Errorf("prune session %q: %w", id, err)
		}
		delete(s.sessions, id)
		pruned = append(pruned, id)
	}
	sort.Strings(pruned)
	return pruned, nil
}

// --- internal helpers (callers hold s.mu) ---

func (s *Store) findOpenByCWDLocked(cwd string) *sessionEntry {
	for _, entry := range s.sessions {
		if entry.session.ClosedAt == nil && entry.session.CWD == cwd {
			return entry
		}
	}
	return nil
}

// uniqueIDLocked generates a random ID not already in use. Collisions are
// astronomically unlikely with 36^8 space but the retry keeps correctness exact.
func (s *Store) uniqueIDLocked() (string, error) {
	for attempt := 0; attempt < 10; attempt++ {
		id, err := newID()
		if err != nil {
			return "", err
		}
		if _, exists := s.sessions[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("generate unique session id: too many collisions")
}

func maxRunNumber(runs []Run) int {
	max := 0
	for _, r := range runs {
		// Run IDs are "r" + number; ignore anything that does not parse.
		if len(r.ID) > 1 && r.ID[0] == 'r' {
			if n, err := strconv.Atoi(r.ID[1:]); err == nil && n > max {
				max = n
			}
		}
	}
	return max
}

// --- defensive copies: never hand out pointers into the live store ---

func cloneSession(in *Session) *Session {
	out := *in
	// Fill the title at the single read boundary so every payload carries a
	// non-empty title without rewriting legacy state files (design D1/D3): a
	// title-less session gets its description-derived fallback here, the stored
	// record stays untouched.
	out.Title = effectiveTitle(in)
	if in.ClosedAt != nil {
		t := *in.ClosedAt
		out.ClosedAt = &t
	}
	if in.Resolution != nil {
		r := *in.Resolution
		if in.Resolution.Guardrail != nil {
			g := *in.Resolution.Guardrail
			r.Guardrail = &g
		}
		out.Resolution = &r
	}
	if in.BlastRadius != nil {
		out.BlastRadius = cloneBlastRadius(in.BlastRadius)
	}
	out.Hypotheses = cloneHypotheses(in.Hypotheses)
	out.Probes = append([]Probe(nil), in.Probes...)
	out.Runs = cloneRuns(in.Runs)
	return &out
}

func cloneHypotheses(in []Hypothesis) []Hypothesis {
	if in == nil {
		return nil
	}
	return append([]Hypothesis(nil), in...)
}

func cloneRuns(in []Run) []Run {
	if in == nil {
		return nil
	}
	out := make([]Run, len(in))
	for i, r := range in {
		out[i] = r
		if r.ClosedAt != nil {
			t := *r.ClosedAt
			out[i].ClosedAt = &t
		}
		if r.PredictionAt != nil {
			t := *r.PredictionAt
			out[i].PredictionAt = &t
		}
	}
	return out
}
