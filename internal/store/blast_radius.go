package store

import (
	"errors"
	"fmt"
	"path/filepath"
	"time"
)

// Blast radius: the sibling-occurrence model + gates (add-blast-radius).
//
// After a root cause is confirmed, the same anti-pattern usually lives at sibling
// call sites. The daemon executes a regex sibling search (D-A) and records the hits
// as facts the agent cannot supply, add, or remove; the store holds the resulting
// radius, enforces the false-coverage gate before storing (D-C), and merges the
// agent's per-hit verdicts (D-D). One radius per session, latest search wins (D-E).
//
// The typed sentinels below follow gates.go's style (D-K): each is wrapped with a
// one-line, actionable repair instruction so the daemon can map the sentinel to a
// 4xx and surface the message verbatim, and the agent repairs the situation rather
// than routing around the error.

// BlastVerdict is the agent's judgment of one recorded sibling hit (D-D).
type BlastVerdict string

const (
	// BlastSiblingBug marks a hit that carries the same defect as the culprit.
	BlastSiblingBug BlastVerdict = "sibling-bug"
	// BlastSafe marks a hit the pattern caught that is not actually buggy.
	BlastSafe BlastVerdict = "safe"
	// BlastAlreadyCovered marks a hit already handled (tested, fixed, or guarded).
	BlastAlreadyCovered BlastVerdict = "already-covered"
)

// blastVerdicts is the closed set of allowed hit verdicts (D-D), kept as a set so
// validation is a single membership check and the error can name the whole set.
var blastVerdicts = map[BlastVerdict]struct{}{
	BlastSiblingBug: {}, BlastSafe: {}, BlastAlreadyCovered: {},
}

// validBlastVerdict reports whether v is one of the allowed hit verdicts (D-D).
func validBlastVerdict(v BlastVerdict) bool {
	_, ok := blastVerdicts[v]
	return ok
}

// BlastHit is one daemon-recorded sibling occurrence (D-A): a file/line the pattern
// matched under the session cwd plus a trimmed excerpt. File is stored cwd-relative.
// Verdict and Note stay empty until AnnotateBlastRadius grades the hit; an empty
// Verdict is reported as "unreviewed" (D-D).
type BlastHit struct {
	File    string       `json:"file"`
	Line    int          `json:"line"`
	Excerpt string       `json:"excerpt,omitempty"`
	Verdict BlastVerdict `json:"verdict,omitempty"`
	Note    string       `json:"note,omitempty"`
}

// BlastRadius is a session's single sibling-occurrence search (D-E: one per session,
// replace-on-re-run). Pattern is the agent-authored regex; Hits are the daemon's
// recorded facts; Truncated discloses the hit cap was reached and is carried onto
// every surface that renders the radius.
type BlastRadius struct {
	Pattern    string     `json:"pattern"`
	Note       string     `json:"note,omitempty"`
	SearchedAt time.Time  `json:"searched_at"`
	Truncated  bool       `json:"truncated"`
	Hits       []BlastHit `json:"hits"`
}

// Unreviewed reports how many recorded hits carry no verdict yet (D-D): every
// rendering surface shows this count so a partial review is never mistaken for a
// clean sweep.
func (r *BlastRadius) Unreviewed() int {
	n := 0
	for i := range r.Hits {
		if r.Hits[i].Verdict == "" {
			n++
		}
	}
	return n
}

// BlastAnnotation is one agent verdict on a recorded hit (D-D): the {file, line}
// identifying the hit plus the verdict and an optional note. It is set-checked
// against the recorded hits before it is applied.
type BlastAnnotation struct {
	File    string
	Line    int
	Verdict BlastVerdict
	Note    string
}

var (
	// ErrNoConfirmedCulprit is returned when a blast radius is mapped before the board
	// has a confirmed hypothesis with a usable confirm citation (D-C): without a
	// confirmed culprit there is no known bug to establish sibling coverage against.
	ErrNoConfirmedCulprit = errors.New("no confirmed root cause to check sibling coverage against")

	// ErrCulpritNotInRadius is returned when the confirmed culprit's file is absent
	// from the search hits (D-C): a pattern that misses the known bug proves nothing
	// about siblings.
	ErrCulpritNotInRadius = errors.New("confirmed culprit is absent from the hit set")

	// ErrNoBlastRadius is returned when annotations arrive before any radius exists.
	ErrNoBlastRadius = errors.New("no blast radius to annotate")

	// ErrUnknownRadiusHit is returned when an annotation cites a {file, line} the
	// search never recorded (D-D): the agent cannot grade sites the daemon did not find.
	ErrUnknownRadiusHit = errors.New("annotation cites a site not in the recorded hits")

	// ErrInvalidBlastVerdict is returned when an annotation carries a verdict outside
	// the allowed enum (D-D).
	ErrInvalidBlastVerdict = errors.New("invalid blast-radius verdict")
)

// normalizeUnderCWD resolves a file path to an absolute path anchored at the session
// cwd so a probe file (absolute or cwd-relative) and a stored hit (cwd-relative)
// compare equal when they name the same source file — the false-coverage gate and
// annotation set-check both compare NORMALIZED against the session cwd. It mirrors
// the daemon's resolveProbePath; the store cannot import the daemon (that is the
// dependency direction), so the tiny helper is duplicated here.
func normalizeUnderCWD(cwd, file string) string {
	if filepath.IsAbs(file) {
		return filepath.Clean(file)
	}
	return filepath.Join(cwd, file)
}

// confirmedCulpritLocked resolves the confirmed culprit's file for the false-coverage
// gate (D-C): the file of the probe cited in the confirmed hypothesis's confirm
// citation (harden-detective-gates D-B). Callers hold s.mu. It returns
// ErrNoConfirmedCulprit when no hypothesis is confirmed, when the confirmed one
// carries no citation, or when its cited probe is unknown — in every case there is
// no known bug to check sibling coverage against.
func confirmedCulpritLocked(sess *Session) (string, error) {
	var confirmed *Hypothesis
	for i := range sess.Hypotheses {
		if sess.Hypotheses[i].Status == HypothesisConfirmed {
			confirmed = &sess.Hypotheses[i]
			break
		}
	}
	if confirmed == nil {
		return "", fmt.Errorf("%w — confirm a hypothesis with cited evidence before mapping its blast radius", ErrNoConfirmedCulprit)
	}
	if confirmed.EvidenceProbeID == "" {
		return "", fmt.Errorf("confirmed hypothesis %q carries no evidence citation, so its culprit file is unknown: %w", confirmed.ID, ErrNoConfirmedCulprit)
	}
	probe := findProbe(sess, confirmed.EvidenceProbeID)
	if probe == nil {
		return "", fmt.Errorf("confirmed hypothesis %q cites probe %q which is not registered: %w", confirmed.ID, confirmed.EvidenceProbeID, ErrNoConfirmedCulprit)
	}
	return probe.File, nil
}

// SetBlastRadius records a daemon-executed sibling search on the session, enforcing
// the false-coverage gate before storing (D-C): the board must hold a confirmed
// hypothesis and the confirmed culprit's probe file must appear in the hits (paths
// compared normalized against the session cwd). It has replace semantics (D-E): a
// new search swaps the whole radius and clears any prior annotations — verdicts
// graded a different search must not carry over. On any gate failure the stored
// radius is left unchanged (fail before mutating).
func (s *Store) SetBlastRadius(sessionID string, radius BlastRadius) (BlastRadius, error) {
	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return BlastRadius{}, fmt.Errorf("set blast radius in %q: %w", sessionID, ErrSessionNotFound)
	}

	culprit, err := confirmedCulpritLocked(entry.session)
	if err != nil {
		s.mu.Unlock()
		return BlastRadius{}, fmt.Errorf("set blast radius in %q: %w", sessionID, err)
	}

	// False-coverage gate (D-C): the culprit file must be among the hits, comparing
	// paths normalized against the session cwd (probe files may be absolute or
	// cwd-relative; hits are cwd-relative).
	culpritNorm := normalizeUnderCWD(entry.session.CWD, culprit)
	matched := false
	for i := range radius.Hits {
		if normalizeUnderCWD(entry.session.CWD, radius.Hits[i].File) == culpritNorm {
			matched = true
			break
		}
	}
	if !matched {
		s.mu.Unlock()
		return BlastRadius{}, fmt.Errorf("set blast radius in %q: the sibling pattern does not match the confirmed culprit at %s — a pattern that misses the known bug cannot establish sibling coverage; broaden it to cover %s and re-run: %w", sessionID, culprit, culprit, ErrCulpritNotInRadius)
	}

	// Replace semantics (D-E): build a fresh radius from the search result, dropping
	// any per-hit verdict/note so annotations from a previous search can never carry
	// over. Excerpts are the search's own facts and are preserved.
	stored := BlastRadius{
		Pattern:    radius.Pattern,
		Note:       radius.Note,
		SearchedAt: radius.SearchedAt,
		Truncated:  radius.Truncated,
		Hits:       make([]BlastHit, len(radius.Hits)),
	}
	if stored.SearchedAt.IsZero() {
		stored.SearchedAt = time.Now().UTC()
	}
	for i := range radius.Hits {
		stored.Hits[i] = BlastHit{
			File:    radius.Hits[i].File,
			Line:    radius.Hits[i].Line,
			Excerpt: radius.Hits[i].Excerpt,
		}
	}

	prev := entry.session.BlastRadius
	entry.session.BlastRadius = &stored
	if err := s.writeState(entry.session); err != nil {
		// Roll back so a failed persist leaves the prior radius intact rather than a
		// phantom one that never reached disk.
		entry.session.BlastRadius = prev
		s.mu.Unlock()
		return BlastRadius{}, err
	}
	out := cloneBlastRadius(&stored)
	s.mu.Unlock()

	s.publish(Event{Kind: EventRadius, Session: sessionID, Payload: *out})
	return *out, nil
}

// AnnotateBlastRadius merges agent verdicts into the recorded radius (D-D). Each
// annotation's {file, line} must match a recorded hit (compared normalized against
// the session cwd) and its verdict must be one of the allowed enum; a single unknown
// site or invalid verdict rejects the whole call with no mutation (fail before
// mutating). Accepted annotations overwrite by {file, line}, so a repeat grade wins,
// and a later verdict wins within a single call too. Hits left without a verdict
// stay unreviewed. Returns the merged radius.
func (s *Store) AnnotateBlastRadius(sessionID string, annotations []BlastAnnotation) (BlastRadius, error) {
	// Pure input validation first, like the board floor: an invalid verdict must never
	// reach the radius.
	for _, a := range annotations {
		if !validBlastVerdict(a.Verdict) {
			return BlastRadius{}, fmt.Errorf("annotate blast radius in %q: verdict %q is not one of sibling-bug, safe, already-covered: %w", sessionID, a.Verdict, ErrInvalidBlastVerdict)
		}
	}

	s.mu.Lock()

	entry, ok := s.sessions[sessionID]
	if !ok {
		s.mu.Unlock()
		return BlastRadius{}, fmt.Errorf("annotate blast radius in %q: %w", sessionID, ErrSessionNotFound)
	}
	radius := entry.session.BlastRadius
	if radius == nil {
		s.mu.Unlock()
		return BlastRadius{}, fmt.Errorf("annotate blast radius in %q: %w — run map_blast_radius first", sessionID, ErrNoBlastRadius)
	}

	// Resolve every annotation to a hit index BEFORE mutating (D-D all-or-nothing): a
	// single unrecorded site rejects the whole call so no annotation is half-applied.
	cwd := entry.session.CWD
	targets := make([]int, len(annotations))
	for i, a := range annotations {
		aNorm := normalizeUnderCWD(cwd, a.File)
		idx := -1
		for j := range radius.Hits {
			if radius.Hits[j].Line == a.Line && normalizeUnderCWD(cwd, radius.Hits[j].File) == aNorm {
				idx = j
				break
			}
		}
		if idx < 0 {
			s.mu.Unlock()
			return BlastRadius{}, fmt.Errorf("annotate blast radius in %q: no recorded hit at %s:%d — grade only sites the search found, or re-run map_blast_radius: %w", sessionID, a.File, a.Line, ErrUnknownRadiusHit)
		}
		targets[i] = idx
	}

	// Apply. Snapshot the hits so a persist failure rolls back cleanly to the exact
	// pre-call state, even when the same hit was graded twice in one call.
	snapshot := append([]BlastHit(nil), radius.Hits...)
	for i, a := range annotations {
		radius.Hits[targets[i]].Verdict = a.Verdict
		radius.Hits[targets[i]].Note = a.Note
	}
	if err := s.writeState(entry.session); err != nil {
		radius.Hits = snapshot
		s.mu.Unlock()
		return BlastRadius{}, err
	}
	out := cloneBlastRadius(radius)
	s.mu.Unlock()

	s.publish(Event{Kind: EventRadius, Session: sessionID, Payload: *out})
	return *out, nil
}

// cloneBlastRadius returns a deep copy so callers never receive a pointer into live
// store state. BlastHit holds no nested pointers, so a slice copy is a full copy.
func cloneBlastRadius(in *BlastRadius) *BlastRadius {
	out := *in
	out.Hits = append([]BlastHit(nil), in.Hits...)
	return &out
}
