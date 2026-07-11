package daemon

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// seedConfirmedRadiusSession stands up a session rooted at cwd whose hypothesis h1 is
// board-confirmed via a real citation on a probe at culprit — the exact precondition
// the false-coverage gate reads (harden-detective-gates D-B/D-C, add-blast-radius D-C).
// Store-level registration is used so the culprit path only has to exist for the walk,
// not pass the daemon's probe-location check.
func seedConfirmedRadiusSession(t *testing.T, srv *Server, st *store.Store, cwd, culprit string) *store.Session {
	t.Helper()
	sess, _, err := st.CreateSession("", "bug", cwd)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.SetHypotheses(sess.ID, []string{"h1 cause", "h2 cause", "h3 cause"}); err != nil {
		t.Fatalf("SetHypotheses: %v", err)
	}
	if _, err := st.RegisterProbe(sess.ID, store.Probe{
		ID: "pc", File: culprit, Line: 1, HypothesisID: "h1",
		ExpectedIfTrue: "token=null past TTL", ExpectedIfFalse: "token fresh",
	}); err != nil {
		t.Fatalf("RegisterProbe: %v", err)
	}
	run := seedRun(t, srv, st, sess.ID, store.VerdictReproduced, "pc")
	if _, err := st.UpdateHypothesisWithEvidence(sess.ID, "h1", store.HypothesisConfirmed, "confirmed by pc", "pc", run); err != nil {
		t.Fatalf("confirm h1: %v", err)
	}
	return sess
}

// getStoredSession fetches a session through the read API and decodes it (the envelope
// promotes the session fields, so blast_radius decodes directly).
func getStoredSession(t *testing.T, srv *Server, id string) store.Session {
	t.Helper()
	w := do(srv, http.MethodGet, "/api/sessions/"+id, "")
	var s store.Session
	decode(t, w, &s)
	return s
}

// TestMapBlastRadiusSuccess covers the happy path (blast-radius spec: "Hits are
// daemon-recorded facts", "Pattern covering the culprit accepted"): a pattern that
// covers the confirmed culprit plus siblings stores every daemon-found hit and reports
// the truncation flag and unreviewed count.
func TestMapBlastRadiusSuccess(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "function auth(){ return readToken() }\n")
	writeFile(t, dir, "src/api.js", "const t = readToken()\n")
	writeFile(t, dir, "src/cli.js", "x\ny\nreadToken()\n")
	writeFile(t, dir, "src/safe.js", "no match here\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken\\(\\)","note":"same token read"}`)
	var got blastRadiusResponse
	decode(t, w, &got)

	if got.Truncated {
		t.Error("small tree should not truncate")
	}
	if got.Pattern != `readToken\(\)` || got.Note != "same token read" {
		t.Errorf("pattern/note not echoed: %+v", got.BlastRadius)
	}
	keys := hitKeys(got.Hits)
	for _, want := range []string{"src/auth.js:1", "src/api.js:1", "src/cli.js:3"} {
		if !keys[want] {
			t.Errorf("missing hit %s; got %v", want, keys)
		}
	}
	if keys["src/safe.js:1"] {
		t.Error("non-matching file must not appear in the hit set")
	}
	if got.UnreviewedCount != len(got.Hits) {
		t.Errorf("unreviewed_count = %d, want all %d hits unreviewed", got.UnreviewedCount, len(got.Hits))
	}
	for _, h := range got.Hits {
		if h.Excerpt == "" {
			t.Errorf("hit %s:%d carries no excerpt", h.File, h.Line)
		}
	}
	// The radius is a recorded fact: it rides the session payload too.
	if s := getStoredSession(t, srv, sess.ID); s.BlastRadius == nil || len(s.BlastRadius.Hits) != len(got.Hits) {
		t.Errorf("stored radius not persisted on the session: %+v", s.BlastRadius)
	}
}

// TestMapBlastRadiusInvalidRegex covers the compile-error path (blast-radius spec:
// "Invalid pattern rejected"): a 400 surfacing the compile error, and no radius stored.
func TestMapBlastRadiusInvalidRegex(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "readToken()\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken("}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "compile") {
		t.Errorf("error body should surface the compile failure verbatim, got %s", w.Body.String())
	}
	if s := getStoredSession(t, srv, sess.ID); s.BlastRadius != nil {
		t.Error("no radius should be stored on a compile failure")
	}
}

// TestMapBlastRadiusNoConfirm covers the gate when nothing is confirmed (blast-radius
// spec: "No confirmed suspect yet"): the call fails 400 stating a confirmed root cause
// is required, and no radius is stored.
func TestMapBlastRadiusNoConfirm(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "readToken()\n")
	// A board exists, but no hypothesis is confirmed.
	sess, _, err := st.CreateSession("", "bug", dir)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.SetHypotheses(sess.ID, []string{"h1", "h2", "h3"}); err != nil {
		t.Fatalf("SetHypotheses: %v", err)
	}

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken\\(\\)"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "confirm") {
		t.Errorf("error should require a confirmed root cause, got %s", w.Body.String())
	}
	if s := getStoredSession(t, srv, sess.ID); s.BlastRadius != nil {
		t.Error("no radius should be stored without a confirmed hypothesis")
	}
}

// TestMapBlastRadiusCulpritMissing covers the false-coverage gate (blast-radius spec:
// "Pattern that misses the culprit rejected"): a pattern the daemon-executed search
// does not find in the culprit file is rejected naming that file, with no radius stored.
func TestMapBlastRadiusCulpritMissing(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	// The culprit file exists but does NOT contain the searched token; siblings do.
	writeFile(t, dir, "src/auth.js", "function auth(){ return decode() }\n")
	writeFile(t, dir, "src/api.js", "const t = readToken()\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken\\(\\)"}`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", w.Code)
	}
	if !strings.Contains(w.Body.String(), "src/auth.js") {
		t.Errorf("rejection should name the culprit file src/auth.js, got %s", w.Body.String())
	}
	if s := getStoredSession(t, srv, sess.ID); s.BlastRadius != nil {
		t.Error("no radius should be stored when the culprit is missing from the hits")
	}
}

// TestMapBlastRadiusRejections covers the small request-shape rejections: empty pattern,
// unknown session, wrong method, and an unknown JSON field (strict readJSON at the door).
func TestMapBlastRadiusRejections(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "readToken()\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")
	base := "/api/sessions/" + sess.ID + "/blast-radius"

	cases := []struct {
		name   string
		method string
		path   string
		body   string
		want   int
	}{
		{"empty pattern", http.MethodPost, base, `{"pattern":"   "}`, http.StatusBadRequest},
		{"unknown field", http.MethodPost, base, `{"patttern":"x"}`, http.StatusBadRequest},
		{"unknown session", http.MethodPost, "/api/sessions/zzzzzzzz/blast-radius", `{"pattern":"x"}`, http.StatusNotFound},
		{"wrong method", http.MethodGet, base, "", http.StatusMethodNotAllowed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(srv, tc.method, tc.path, tc.body)
			if w.Code != tc.want {
				t.Errorf("status = %d, want %d (body=%s)", w.Code, tc.want, w.Body.String())
			}
		})
	}
}

// TestMapBlastRadiusTruncation covers the endpoint surfacing truncation (blast-radius
// spec: "Truncated scan disclosed"): a broad pattern over more than the cap's worth of
// sites stores the cap and reports truncated=true. The culprit sorts first so it is
// recorded before the cap is reached and the gate passes.
func TestMapBlastRadiusTruncation(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "a_auth.js", "readToken()\n") // culprit, visited first (lexical order)
	var b strings.Builder
	for i := 0; i < blastHitCap+50; i++ {
		b.WriteString("readToken()\n")
	}
	writeFile(t, dir, "zz_many.js", b.String())
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "a_auth.js")

	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken"}`)
	var got blastRadiusResponse
	decode(t, w, &got)

	if !got.Truncated {
		t.Error("want truncated=true for a pattern exceeding the hit cap")
	}
	if len(got.Hits) != blastHitCap {
		t.Errorf("recorded %d hits, want the cap of %d", len(got.Hits), blastHitCap)
	}
	if !hitKeys(got.Hits)["a_auth.js:1"] {
		t.Error("culprit must be among the recorded hits despite truncation")
	}
}

// TestAnnotateBlastRadius covers grading recorded hits through the endpoint
// (blast-radius spec: "Annotations are set-checked", "Partial review disclosed"):
// a valid partial annotation updates the verdict and shrinks the unreviewed count.
func TestAnnotateBlastRadius(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "readToken()\n")
	writeFile(t, dir, "src/api.js", "readToken()\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")

	if w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken\\(\\)"}`); w.Code != http.StatusOK {
		t.Fatalf("map status = %d", w.Code)
	}

	body := `{"annotations":[{"file":"src/api.js","line":1,"verdict":"sibling-bug","note":"same defect"}]}`
	w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius/annotations", body)
	var got blastRadiusResponse
	decode(t, w, &got)

	if got.UnreviewedCount != 1 {
		t.Errorf("unreviewed_count = %d, want 1 (auth.js still unreviewed)", got.UnreviewedCount)
	}
	var graded bool
	for _, h := range got.Hits {
		if h.File == "src/api.js" && h.Line == 1 {
			graded = true
			if h.Verdict != store.BlastSiblingBug || h.Note != "same defect" {
				t.Errorf("api.js verdict/note not applied: %+v", h)
			}
		}
	}
	if !graded {
		t.Error("annotated hit src/api.js not found in the response")
	}
}

// TestAnnotateBlastRadiusRejections covers the annotation rejection paths (blast-radius
// spec: "Annotation of an unrecorded site rejected"; mcp-server daemon-side verdict
// validation): an unknown site, an invalid verdict, and an annotate before any search
// are all 400s, plus method/shape guards.
func TestAnnotateBlastRadiusRejections(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "src/auth.js", "readToken()\n")
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "src/auth.js")
	annPath := "/api/sessions/" + sess.ID + "/blast-radius/annotations"

	// Annotate before any radius exists → 400.
	if w := do(srv, http.MethodPost, annPath, `{"annotations":[{"file":"src/auth.js","line":1,"verdict":"safe"}]}`); w.Code != http.StatusBadRequest {
		t.Fatalf("annotate before search status = %d, want 400", w.Code)
	}

	// Map a radius so subsequent rejections are about the annotation, not a missing radius.
	if w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", `{"pattern":"readToken\\(\\)"}`); w.Code != http.StatusOK {
		t.Fatalf("map status = %d", w.Code)
	}

	cases := []struct {
		name   string
		method string
		body   string
		want   int
		expect string // substring required in the error body (empty ⇒ skip)
	}{
		{"unknown site", http.MethodPost, `{"annotations":[{"file":"src/other.js","line":9,"verdict":"safe"}]}`, http.StatusBadRequest, "src/other.js"},
		{"invalid verdict", http.MethodPost, `{"annotations":[{"file":"src/auth.js","line":1,"verdict":"probably-fine"}]}`, http.StatusBadRequest, ""},
		{"unknown field", http.MethodPost, `{"annotations":[{"file":"src/auth.js","line":1,"verdict":"safe","reason":"x"}]}`, http.StatusBadRequest, ""},
		{"wrong method", http.MethodGet, "", http.StatusMethodNotAllowed, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := do(srv, tc.method, annPath, tc.body)
			if w.Code != tc.want {
				t.Fatalf("status = %d, want %d (body=%s)", w.Code, tc.want, w.Body.String())
			}
			if tc.expect != "" && !strings.Contains(w.Body.String(), tc.expect) {
				t.Errorf("error body should name %q, got %s", tc.expect, w.Body.String())
			}
		})
	}
}

// TestBlastRadiusScanDoesNotBlockIngestOrAwait is the concurrency guard (blast-radius
// spec: "The scan SHALL NOT block event ingest or waiting await_run"; design D-G — the
// walk runs outside the store lock). It builds a tree large enough that the walk visits
// many files (no early truncation, so it reliably outlasts a single store op) and proves
// (1) a concurrent ingest returns while the walk is still running, and (2) an open await
// still observes ingested events during a concurrent scan.
func TestBlastRadiusScanDoesNotBlockIngestOrAwait(t *testing.T) {
	srv, st := newTestServer(t)
	dir := t.TempDir()
	writeFile(t, dir, "a_auth.js", "readToken()\n") // the only matching (culprit) file
	// Thousands of non-matching files force the walk to visit the whole tree without
	// truncating, so its filesystem work far outlasts a single in-memory store op. If the
	// walk held the store lock, the concurrent ingest below would serialize behind it.
	for i := 0; i < 3000; i++ {
		writeFile(t, dir, fmt.Sprintf("tree/f%04d.txt", i), "nothing to match here\n")
	}
	sess := seedConfirmedRadiusSession(t, srv, st, dir, "a_auth.js")
	pattern := `{"pattern":"readToken\\(\\)"}`

	// (1) Deterministic non-blocking guard: a single ingest issued right after the scan
	// starts must return while the multi-thousand-file walk is still running.
	scan1 := make(chan int, 1)
	go func() {
		w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", pattern)
		scan1 <- w.Code
	}()
	if w := do(srv, http.MethodPost, "/log/"+sess.ID+"/pc", `{"i":1}`); w.Code != http.StatusOK {
		t.Fatalf("concurrent ingest status = %d, want 200", w.Code)
	}
	select {
	case code := <-scan1:
		t.Fatalf("scan finished (code %d) before a concurrent ingest returned — the walk must run outside the store lock so ingest is never serialized behind it", code)
	default:
		// good: the scan is still walking, so the ingest did not wait on it
	}
	if code := <-scan1; code != http.StatusOK {
		t.Fatalf("blast-radius scan status = %d, want 200", code)
	}

	// (2) An open await is not blocked by a concurrent scan. Pre-attribute events to an
	// open run, then run a scan and an await in parallel; the await must return with its
	// summary intact within a generous deadline.
	if _, err := st.OpenRun(sess.ID); err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	for i := 0; i < 3; i++ {
		if w := do(srv, http.MethodPost, "/log/"+sess.ID+"/pc", `{"i":1}`); w.Code != http.StatusOK {
			t.Fatalf("seed ingest status = %d", w.Code)
		}
	}
	scan2 := make(chan int, 1)
	go func() {
		w := do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/blast-radius", pattern)
		scan2 <- w.Code
	}()
	awaitCh := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		awaitCh <- do(srv, http.MethodPost, "/api/sessions/"+sess.ID+"/await", `{"timeout_s":5}`)
	}()
	select {
	case w := <-awaitCh:
		if w.Code != http.StatusOK {
			t.Fatalf("await during scan status = %d, want 200", w.Code)
		}
		var res awaitResult
		if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
			t.Fatalf("decode await result: %v", err)
		}
		var pc int
		for _, ps := range res.Summary {
			if ps.Probe == "pc" {
				pc = ps.Total
			}
		}
		if pc < 3 {
			t.Errorf("await summary saw pc=%d events, want ≥3 despite the concurrent scan", pc)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("await did not return within 10s during a concurrent scan — the scan may be blocking the await")
	}
	if code := <-scan2; code != http.StatusOK {
		t.Fatalf("second scan status = %d, want 200", code)
	}
}
