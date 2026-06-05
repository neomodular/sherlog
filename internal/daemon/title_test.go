package daemon

import (
	"net/http"
	"strings"
	"testing"

	"github.com/neomodular/sherlog/internal/store"
)

// TestCreateSessionEchoesTitle covers mcp-server scenario "Titled start" at the
// daemon boundary: POST /api/sessions with a title creates the session with that
// title and the response carries it back distinctly from the description.
func TestCreateSessionEchoesTitle(t *testing.T) {
	srv, _ := newTestServer(t)

	w := do(srv, http.MethodPost, "/api/sessions",
		`{"title":"Cart total off by cents","description":"Symptom: totals end in .x9","cwd":"/repo"}`)
	var body struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &body)
	if body.Session.Title != "Cart total off by cents" {
		t.Errorf("create response title = %q, want the supplied title", body.Session.Title)
	}
	if body.Session.Description != "Symptom: totals end in .x9" {
		t.Errorf("description altered: %q", body.Session.Description)
	}
}

// TestCreateSessionDerivesTitleWhenOmitted covers mcp-server scenario "Legacy
// caller": a create with only a description still yields a non-empty derived title
// in the response (word-boundary truncation for a long description).
func TestCreateSessionDerivesTitleWhenOmitted(t *testing.T) {
	srv, _ := newTestServer(t)

	long := "the login endpoint returns a 401 on the very first request after the browser tab has been idle for several minutes"
	w := do(srv, http.MethodPost, "/api/sessions",
		`{"description":"`+long+`","cwd":"/repo"}`)
	var body struct {
		Session *store.Session `json:"session"`
	}
	decode(t, w, &body)
	if body.Session.Title == "" {
		t.Fatal("omitted title must be derived; response carried an empty title")
	}
	if !strings.HasPrefix(long, strings.TrimSuffix(body.Session.Title, "…")) {
		t.Errorf("derived title %q is not a description prefix", body.Session.Title)
	}
}

// TestCasesEndpointCarriesTitle covers case-board-ui: the case list payload exposes
// each session's title (real or derived) so the browser list can show it.
func TestCasesEndpointCarriesTitle(t *testing.T) {
	srv, st := newTestServer(t)
	if _, _, err := st.CreateSession("Login 401 after idle timeout", "a long detailed description here", "/repo"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	w := do(srv, http.MethodGet, "/api/cases", "")
	var cases []store.Session
	decode(t, w, &cases)
	if len(cases) != 1 {
		t.Fatalf("want 1 case, got %d", len(cases))
	}
	if cases[0].Title != "Login 401 after idle timeout" {
		t.Errorf("case list title = %q, want the supplied title", cases[0].Title)
	}
}

// TestStaleProbesCarrySessionTitle covers case-board-ui: stale-probes rows identify
// the owning case by title, so the payload must include a non-empty session_title.
func TestStaleProbesCarrySessionTitle(t *testing.T) {
	srv, st := newTestServer(t)
	sess, _, err := st.CreateSession("Kerberos ticket renewal fails", "auth dies overnight", "/repo")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := st.RegisterProbe(sess.ID, store.Probe{ID: "p1", File: "auth.go", Line: 10, HypothesisID: "h1"}); err != nil {
		t.Fatalf("RegisterProbe: %v", err)
	}

	w := do(srv, http.MethodGet, "/api/probes/stale", "")
	var stale []store.StaleProbe
	decode(t, w, &stale)
	if len(stale) != 1 {
		t.Fatalf("want 1 stale probe, got %d", len(stale))
	}
	if stale[0].SessionTitle != "Kerberos ticket renewal fails" {
		t.Errorf("stale probe session_title = %q, want the case title", stale[0].SessionTitle)
	}
}
