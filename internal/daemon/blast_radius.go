package daemon

import (
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// The internal /api/ surface for the blast radius (add-blast-radius, mcp-server
// spec). Two POST endpoints under a session, driven by the MCP process:
//
//	POST /api/sessions/<id>/blast-radius              → compile + walk + store the search
//	POST /api/sessions/<id>/blast-radius/annotations  → grade recorded hits
//
// The daemon is the search executor (D-A): the walk runs here, outside the store
// lock, and only the resulting facts reach store.SetBlastRadius, which enforces the
// false-coverage gate (D-C) before persisting. Gate and validation rejections carry a
// one-line repair instruction and surface verbatim as 400s via handleStoreErr (D-K).

// blastRadiusResponse is the map/annotate endpoint payload: the stored radius plus its
// derived unreviewed count so the MCP tool need not recompute it. It mirrors
// sessionEnvelope's additive-embedding style — the embedded BlastRadius promotes its
// fields, so pattern/hits/truncated decode directly on the wire. The field is named
// UnreviewedCount to avoid colliding with the promoted Unreviewed() method.
type blastRadiusResponse struct {
	store.BlastRadius
	UnreviewedCount int `json:"unreviewed_count"`
}

// newBlastRadiusResponse derives the unreviewed count once so every surface agrees on
// it (D-D): a partial review is never mistaken for a clean sweep.
func newBlastRadiusResponse(r store.BlastRadius) blastRadiusResponse {
	return blastRadiusResponse{BlastRadius: r, UnreviewedCount: r.Unreviewed()}
}

// mapBlastRadius runs the sibling search: it compiles the agent-authored pattern,
// walks the session cwd, and records the hits under the false-coverage gate (D-A/D-C).
// A compile failure and an empty pattern are client errors surfaced verbatim; the walk
// runs outside any store lock (D-G); the gate rejection (no confirmed hypothesis, or a
// pattern that misses the culprit file) is a 400 with the repair instruction.
func (s *Server) mapBlastRadius(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Pattern string `json:"pattern"`
		Note    string `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Pattern) == "" {
		writeError(w, http.StatusBadRequest, errors.New("blast radius requires a non-empty search pattern"))
		return
	}
	// Session lookup first so an unknown session is a clean 404 and the walk has a cwd
	// to root at, rather than walking an empty path.
	sess, err := s.store.GetSession(id)
	if s.handleStoreErr(w, err) {
		return
	}
	// Compile with the stdlib RE2 engine (D-A): a compile failure is the agent's to fix,
	// so surface it verbatim as a 400. RE2 guarantees linear-time matching, so an
	// untrusted pattern cannot wedge the daemon (D-G).
	re, err := regexp.Compile(req.Pattern)
	if err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("blast radius pattern failed to compile: %w", err))
		return
	}
	// Walk the cwd OUTSIDE the store lock (D-G): the scan is pure filesystem work, so a
	// long walk never blocks /log/ ingest or an open await_run.
	hits, truncated, err := walkBlastRadius(sess.CWD, re)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	radius := store.BlastRadius{
		Pattern:    req.Pattern,
		Note:       req.Note,
		SearchedAt: time.Now().UTC(),
		Truncated:  truncated,
		Hits:       hits,
	}
	// SetBlastRadius enforces the false-coverage gate (D-C) and replace semantics (D-E)
	// under its own lock; a rejection surfaces verbatim as a 400 via handleStoreErr.
	stored, err := s.store.SetBlastRadius(id, radius)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, newBlastRadiusResponse(stored))
}

// annotateBlastRadius merges the agent's per-hit verdicts into the recorded radius
// (D-D). The store validates each verdict against the enum and set-checks each
// {file, line} against the recorded hits, rejecting the whole call (400) on an unknown
// site or invalid verdict — the agent cannot grade sites the search did not find.
func (s *Server) annotateBlastRadius(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Annotations []struct {
			File    string `json:"file"`
			Line    int    `json:"line"`
			Verdict string `json:"verdict"`
			Note    string `json:"note"`
		} `json:"annotations"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	anns := make([]store.BlastAnnotation, len(req.Annotations))
	for i, a := range req.Annotations {
		anns[i] = store.BlastAnnotation{
			File:    a.File,
			Line:    a.Line,
			Verdict: store.BlastVerdict(a.Verdict),
			Note:    a.Note,
		}
	}
	stored, err := s.store.AnnotateBlastRadius(id, anns)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, newBlastRadiusResponse(stored))
}
