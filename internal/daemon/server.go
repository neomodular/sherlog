package daemon

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/neomodular/sherlog/internal/notes"
	"github.com/neomodular/sherlog/internal/store"
)

// maxIngestBody caps a single probe body read so a misbehaving probe cannot
// exhaust daemon memory. Probe payloads are small debug snapshots, not uploads.
const maxIngestBody = 1 << 20 // 1 MiB

// maxAPIBody caps a single internal /api/ request body read. Distinct from
// maxIngestBody (probe ingest) so the two concerns can diverge independently;
// API requests are small JSON command payloads.
const maxAPIBody = 1 << 20 // 1 MiB

// Server is the daemon's HTTP surface: the public ingest/CORS/health endpoints
// that probes and the skill hit, plus the internal /api/ endpoints the MCP
// process calls (D2). It owns no mutable state of its own; the store is the
// concurrency-safe source of truth, so the Server is safe for concurrent use.
type Server struct {
	store   *store.Store
	notes   *notes.Store
	awaiter *awaitEngine
	version string
	started time.Time
	mux     *http.ServeMux
}

// NewServer builds the Server and its route table over the given store. The
// field-notes store shares the investigation store's root so all local sherlog
// data lives in one directory (field-notes design D1); a failure to construct it
// is non-fatal — notes are best-effort telemetry, never an investigation gate.
func NewServer(s *store.Store, version string) *Server {
	srv := &Server{
		store:   s,
		awaiter: newAwaitEngine(s),
		version: version,
		started: time.Now(),
		mux:     http.NewServeMux(),
	}
	if n, err := notes.New(notes.WithRoot(s.Root())); err == nil {
		srv.notes = n
	}
	srv.routes()
	return srv
}

// ServeHTTP dispatches to the route table. CORS is applied only by the public
// probe/health handlers (D3); the internal /api/ surface is a same-origin
// server-side client (the MCP process, D2) and must NOT advertise cross-origin
// access, so it carries no CORS headers — otherwise any website the developer
// visits could read or mutate investigation state cross-origin.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

func (s *Server) routes() {
	// Public surface.
	s.mux.HandleFunc("/log/", s.handleIngest)
	s.mux.HandleFunc("/health", s.handleHealth)

	// Internal API for the MCP process (D2, D4). Kept under /api/ so it is
	// visibly distinct from the public probe/health endpoints.
	s.mux.HandleFunc("/api/sessions", s.handleSessions)     // POST create, GET resume-latest
	s.mux.HandleFunc("/api/sessions/", s.handleSessionByID) // GET state, DELETE close, plus sub-resources
	s.mux.HandleFunc("/api/notes", s.handleNotes)           // POST file a field note (field-notes D2)
}

// --- Public: log ingest (D3, spec: Localhost HTTP log ingestion) ---

// handleIngest stores a probe hit. It is intentionally permissive: it ignores
// Content-Type, parses the body as JSON opportunistically with a raw-string
// fallback, accepts empty bodies, and answers 200 for any well-routed request
// — a probe can never fail validation (D3). Unknown sessions are dropped
// silently (still 200) to neutralize drive-by localhost POSTs (D4).
func (s *Server) handleIngest(w http.ResponseWriter, r *http.Request) {
	// CORS belongs only to the public probe surface so browser probes work from
	// any origin (D3); /api/ deliberately carries no CORS headers.
	setCORS(w)
	if r.Method == http.MethodOptions {
		// Preflight: answer success with the headers that permit the real POST.
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if r.Method != http.MethodPost {
		// Only POST ingests; other verbs get a clean 405.
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	sessionID, probeID, ok := parseLogPath(r.URL.Path)
	if !ok {
		// Malformed path is not a routed ingest; nothing to store.
		w.WriteHeader(http.StatusNotFound)
		return
	}

	raw, err := io.ReadAll(io.LimitReader(r.Body, maxIngestBody))
	if err != nil {
		// A read error still must not break the probe contract: 200 and drop.
		w.WriteHeader(http.StatusOK)
		return
	}

	body, rawStr := decodeBody(raw)
	// Drop unknown-session events silently; any other store error is also
	// swallowed at the response layer so the probe never observes a failure.
	_ = s.store.Ingest(sessionID, probeID, body, rawStr)
	w.WriteHeader(http.StatusOK)
}

// parseLogPath extracts the session and probe IDs from /log/<session>/<probe>.
// The path must have exactly those two segments: trailing segments are rejected
// rather than folded into the probe ID, which would create a distinct flood
// bucket from the canonical /log/<session>/<probe>.
func parseLogPath(path string) (session, probe string, ok bool) {
	rest := strings.TrimPrefix(path, "/log/")
	if rest == path {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// decodeBody implements tolerant body parsing (D3): an empty body yields no
// content; a body that parses as JSON is stored as the parsed value; anything
// else is stored verbatim as a raw string. Returns (parsedBody, rawString) —
// exactly one is non-zero, or both zero for an empty body. Whitespace is used
// only to detect emptiness; a non-empty raw body is stored unaltered so a probe
// can include meaningful leading/trailing whitespace.
func decodeBody(raw []byte) (body any, rawStr string) {
	if strings.TrimSpace(string(raw)) == "" {
		return nil, ""
	}
	var parsed any
	if err := json.Unmarshal(raw, &parsed); err == nil {
		return parsed, ""
	}
	return nil, string(raw)
}

// --- Public: health (spec: Health endpoint) ---

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	// Public connectivity-check surface: permissive CORS, GET/HEAD only (spec
	// scenario is GET /health) for method-gating consistency with other handlers.
	setCORS(w)
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"version": s.version,
		"uptime":  time.Since(s.started).Round(time.Second).String(),
	})
}

// --- Internal API (D2): /api/sessions and /api/sessions/<id>/... ---

func (s *Server) handleSessions(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		s.createSession(w, r)
	case http.MethodGet:
		s.resumeLatest(w, r)
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Description string `json:"description"`
		CWD         string `json:"cwd"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	created, existing, err := s.store.CreateSession(req.Description, req.CWD)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"session":           created,
		"existing_same_cwd": existing, // non-nil warns the caller of a concurrent session
	})
}

func (s *Server) resumeLatest(w http.ResponseWriter, r *http.Request) {
	sess, err := s.store.ResumeLatest()
	if errors.Is(err, store.ErrNoOpenSession) {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, sess)
}

// handleSessionByID dispatches /api/sessions/<id> and its sub-resources:
//
//	GET    /api/sessions/<id>                 → full state
//	DELETE /api/sessions/<id>                 → close, returns unremoved probes
//	PUT    /api/sessions/<id>/hypotheses      → set board
//	PATCH  /api/sessions/<id>/hypotheses/<hid>→ update status/note
//	POST   /api/sessions/<id>/probes          → register probe
//	DELETE /api/sessions/<id>/probes/<pid>    → mark removed
//	POST   /api/sessions/<id>/await           → open-or-attach run + blocking wait
//	POST   /api/sessions/<id>/runs/close      → record verdict on latest open run
//	GET    /api/sessions/<id>/query           → filtered logs
func (s *Server) handleSessionByID(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/sessions/")
	if rest == "" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	parts := strings.Split(rest, "/")
	sessionID := parts[0]
	sub := parts[1:]

	switch {
	case len(sub) == 0:
		s.sessionRoot(w, r, sessionID)
	case sub[0] == "hypotheses" && len(sub) == 1:
		s.setHypotheses(w, r, sessionID)
	case sub[0] == "hypotheses" && len(sub) == 2:
		s.updateHypothesis(w, r, sessionID, sub[1])
	case sub[0] == "probes" && len(sub) == 1:
		s.registerProbe(w, r, sessionID)
	case sub[0] == "probes" && len(sub) == 2:
		s.removeProbe(w, r, sessionID, sub[1])
	case sub[0] == "await" && len(sub) == 1:
		s.awaitRun(w, r, sessionID)
	case sub[0] == "runs" && len(sub) == 2 && sub[1] == "close":
		// /runs/close closes the latest open run; verdict in the body.
		s.closeRun(w, r, sessionID)
	case sub[0] == "query" && len(sub) == 1:
		s.queryLogs(w, r, sessionID)
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (s *Server) sessionRoot(w http.ResponseWriter, r *http.Request, id string) {
	switch r.Method {
	case http.MethodGet:
		sess, err := s.store.GetSession(id)
		if errors.Is(err, store.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, sess)
	case http.MethodDelete:
		unremoved, err := s.store.CloseSession(id)
		if errors.Is(err, store.ErrSessionNotFound) {
			writeError(w, http.StatusNotFound, err)
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"unremoved_probes": unremoved})
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *Server) setHypotheses(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPut {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Statements []string `json:"statements"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	board, err := s.store.SetHypotheses(id, req.Statements)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, board)
}

func (s *Server) updateHypothesis(w http.ResponseWriter, r *http.Request, id, hid string) {
	if r.Method != http.MethodPatch {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Status string `json:"status"`
		Note   string `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	h, err := s.store.UpdateHypothesis(id, hid, store.HypothesisStatus(req.Status), req.Note)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, h)
}

func (s *Server) registerProbe(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var p store.Probe
	if !readJSON(w, r, &p) {
		return
	}
	saved, err := s.store.RegisterProbe(id, p)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, saved)
}

func (s *Server) removeProbe(w http.ResponseWriter, r *http.Request, id, pid string) {
	if r.Method != http.MethodDelete {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if err := s.store.RemoveProbe(id, pid); s.handleStoreErr(w, err) {
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) awaitRun(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		TimeoutS int `json:"timeout_s"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	timeout := defaultAwaitTimeout
	if req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	// Clamp client-supplied timeouts so a misconfigured caller cannot hold a
	// daemon goroutine open far longer than any real reproduction needs.
	if timeout > maxAwaitTimeout {
		timeout = maxAwaitTimeout
	}
	res, err := s.awaiter.await(r.Context(), id, timeout)
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) closeRun(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var req struct {
		Verdict string `json:"verdict"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	// Close atomically under one lock (D7): the latest-open lookup and the close
	// are a single store op, so a concurrent close can't strand this caller on a
	// stale run. No open run is a 409, not a 500.
	closed, err := s.store.CloseLatestOpenRun(id, store.RunVerdict(req.Verdict))
	if errors.Is(err, store.ErrNoOpenRun) {
		writeError(w, http.StatusConflict, err)
		return
	}
	if s.handleStoreErr(w, err) {
		return
	}
	writeJSON(w, http.StatusOK, closed)
}

func (s *Server) queryLogs(w http.ResponseWriter, r *http.Request, id string) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	f := store.QueryFilter{
		Run:   q.Get("run"),
		Probe: q.Get("probe"),
		Limit: limit,
	}
	results, err := s.store.QueryLogs(id, f)
	if s.handleStoreErr(w, err) {
		return
	}
	// "Did a probe ever fire" (spec: log-query): a probe filter that matched no
	// bucket must report an explicit count of 0, not an empty array, so the skill
	// can distinguish "fired zero times" from "no data". The store contract stays
	// fired-only; the zero record is synthesized here.
	if f.Probe != "" && len(results) == 0 {
		results = []store.QueryResult{{Probe: f.Probe, Run: f.Run, Total: 0, Truncated: false, Events: []store.LogEvent{}}}
	}
	writeJSON(w, http.StatusOK, results)
}

// --- Internal API: field notes (field-notes D2) ---

// handleNotes appends one agent observation about sherlog itself to the global
// field-notes file. The internal /api/ surface carries no CORS (server-side MCP
// client only). The endpoint stamps the note with the daemon's version so the
// note records the build that filed it. Filing failures are returned as errors so
// the MCP tool can swallow them at its boundary (fire-and-forget lives in the
// tool/skill, design D3), but a note never blocks any investigation endpoint.
func (s *Server) handleNotes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if s.notes == nil {
		// Notes store unavailable (root could not resolve): degrade silently rather
		// than 500, since field notes are best-effort telemetry, never required.
		writeError(w, http.StatusServiceUnavailable, errors.New("field notes unavailable"))
		return
	}
	var req struct {
		Session  string `json:"session"`
		Category string `json:"category"`
		Note     string `json:"note"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	n, err := s.notes.Append(req.Session, s.version, notes.Category(req.Category), req.Note)
	if errors.Is(err, notes.ErrInvalidCategory) {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, n)
}

// --- shared helpers ---

func setCORS(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Access-Control-Allow-Origin", "*")
	h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
	h.Set("Access-Control-Allow-Headers", "*")
}

// handleStoreErr maps store errors to HTTP status and reports whether the
// request was already answered. Not-found errors become 404; anything else 500.
func (s *Server) handleStoreErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, store.ErrSessionNotFound),
		errors.Is(err, store.ErrHypothesisNotFound),
		errors.Is(err, store.ErrProbeNotFound),
		errors.Is(err, store.ErrRunNotFound):
		writeError(w, http.StatusNotFound, err)
	default:
		writeError(w, http.StatusInternalServerError, err)
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

// readJSON decodes a request body into v, answering 400 on malformed JSON. An
// empty body is treated as an empty object so callers with all-optional fields
// (e.g. await with default timeout) need not send one.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxAPIBody))
	if err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		return true
	}
	if err := json.Unmarshal(raw, v); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return false
	}
	return true
}
