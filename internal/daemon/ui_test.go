package daemon

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
)

// uiPaths are the embedded Case Board assets that must serve from the binary
// (case-board-ui spec: served by the daemon, all assets embedded). Each is checked
// for a 200 and a sensible content-type so a renamed or dropped asset fails CI.
var uiPaths = []struct {
	path        string
	contentType string // substring the served Content-Type must contain
}{
	{"/", "text/html"},
	{"/board.css", "text/css"},
	{"/board.js", "javascript"},
	{"/api.js", "javascript"},
	{"/render.js", "javascript"},
	{"/cases.js", "javascript"},
	{"/detail.js", "javascript"},
	{"/diff.js", "javascript"},
	{"/stale.js", "javascript"},
	{"/health.js", "javascript"},
}

// TestCaseBoardServed covers the Case Board serving guarantee (case-board-ui spec:
// Open the Case Board): GET / and every asset returns 200 from embedded files with
// the right content-type — no filesystem dependency at runtime.
func TestCaseBoardServed(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, a := range uiPaths {
		w := do(srv, http.MethodGet, a.path, "")
		if w.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200", a.path, w.Code)
			continue
		}
		if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, a.contentType) {
			t.Errorf("GET %s content-type = %q, want to contain %q", a.path, ct, a.contentType)
		}
	}
}

// TestCaseBoardSPAFallback covers the hash-router deep-link case (design D7): an
// unknown UI path (no real file, not an API route) serves index.html so a reload on
// a client-side route lands on the app, not a 404.
func TestCaseBoardSPAFallback(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/case/abc123", "")
	if w.Code != http.StatusOK {
		t.Fatalf("GET /case/abc123 = %d, want 200 (SPA fallback)", w.Code)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("SPA fallback content-type = %q, want html", ct)
	}
	// The served document must reference its entry assets by root-absolute path. A
	// relative href would resolve against the deep-link path (e.g. /case/board.js),
	// hit the SPA fallback again, and serve HTML instead of the module — a blank app.
	body := w.Body.String()
	for _, ref := range []string{`href="/board.css"`, `src="/board.js"`} {
		if !strings.Contains(body, ref) {
			t.Errorf("SPA fallback body missing %s; deep-link reload would fail to load assets", ref)
		}
	}
}

// TestCaseBoardReadOnly covers the read-only UI guarantee for the root handler
// (case-board-ui spec: Read-only UI): the Case Board rejects every write verb, so
// the SPA can never expose a mutation surface.
func TestCaseBoardReadOnly(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		w := do(srv, m, "/", "")
		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("%s / = %d, want 405 (read-only UI)", m, w.Code)
		}
	}
}

// externalURL matches any http(s) URL whose host is not loopback. The Case Board
// must issue zero external requests (case-board-ui spec; design D1: no CDN), so the
// only absolute URLs allowed in the bundle are 127.0.0.1 references (e.g. example
// probe URLs in copy). Anything else means an asset reaches off-machine.
var externalURL = regexp.MustCompile(`https?://(?:[^\s"'<>)]+)`)

// TestCaseBoardNoExternalURLs greps every embedded asset for absolute http(s) URLs
// and fails on any host other than 127.0.0.1 (case-board-ui spec: zero external
// requests). This catches an accidentally pasted CDN link, web font, or analytics
// beacon before it ships in the binary.
func TestCaseBoardNoExternalURLs(t *testing.T) {
	srv, _ := newTestServer(t)
	for _, a := range uiPaths {
		w := do(srv, http.MethodGet, a.path, "")
		body := readBody(t, w)
		for _, m := range externalURL.FindAllString(body, -1) {
			if !strings.HasPrefix(m, "http://127.0.0.1") && !strings.HasPrefix(m, "https://127.0.0.1") {
				t.Errorf("asset %s references external URL %q (must be 127.0.0.1 only)", a.path, m)
			}
		}
	}
}

// readBody drains a recorder's body to a string for content assertions.
func readBody(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	b, err := io.ReadAll(w.Result().Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(b)
}
