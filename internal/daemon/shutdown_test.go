package daemon

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// startRun boots the real daemon via Run on a SHERLOG_PORT-selected free port
// over a temp-dir home, returning the base URL and Run's result channel. It
// fails the test if the daemon does not answer /health in time — the shutdown
// tests need a genuinely running Run loop, not just a Server handler, because
// the drain plumbing under test lives in Run (daemon-self-heal-on-upgrade D3).
func startRun(t *testing.T) (base string, errCh chan error) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	port := freeLoopbackPort(t)
	t.Setenv("SHERLOG_PORT", port)

	errCh = make(chan error, 1)
	go func() { errCh <- Run("test") }()

	base = "http://" + net.JoinHostPort("127.0.0.1", port)
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Run returned early: %v", err)
		default:
		}
		resp, err := http.Get(base + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return base, errCh
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not become healthy on %s", base)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestShutdownAckThenExit covers the core handshake (spec: Shutdown acknowledged
// then executed): POST /api/shutdown answers 200 {"ok":true}, Run returns nil
// within the drain budget, and the port stops accepting connections.
func TestShutdownAckThenExit(t *testing.T) {
	base, errCh := startRun(t)

	resp, err := http.Post(base+"/api/shutdown", "application/json", nil)
	if err != nil {
		t.Fatalf("POST /api/shutdown: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("shutdown status = %d, want 200", resp.StatusCode)
	}
	var ack struct {
		OK bool `json:"ok"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&ack); err != nil || !ack.OK {
		t.Fatalf("shutdown ack = %+v (err %v), want {ok:true}", ack, err)
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned %v after shutdown, want nil (exit 0)", err)
		}
	case <-time.After(shutdownDrainBudget + 2*time.Second):
		t.Fatal("Run did not return within the drain budget after shutdown")
	}

	// The port must be released: a fresh dial is refused once Run has returned.
	if _, err := http.Get(base + "/health"); err == nil {
		t.Error("daemon still answering /health after shutdown completed")
	}
}

// TestShutdownCutsHeldLongPoll covers the bounded-drain requirement (spec:
// Long-poll cannot stall shutdown): an await_run long-poll held open across the
// shutdown must not delay Run's exit beyond the drain budget.
func TestShutdownCutsHeldLongPoll(t *testing.T) {
	base, errCh := startRun(t)

	// Open a session so the await has something to attach to.
	body, _ := json.Marshal(map[string]string{"title": "t", "description": "long poll", "cwd": "/x"})
	resp, err := http.Post(base+"/api/sessions", "application/json", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	var created struct {
		Session struct {
			ID string `json:"id"`
		} `json:"session"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	resp.Body.Close()

	// Hold a long await open; its response (or cut) is irrelevant to the test.
	awaitStarted := make(chan struct{})
	go func() {
		close(awaitStarted)
		b, _ := json.Marshal(map[string]int{"timeout_s": 60})
		r, err := http.Post(base+"/api/sessions/"+created.Session.ID+"/await", "application/json", bytes.NewReader(b))
		if err == nil {
			r.Body.Close()
		}
	}()
	<-awaitStarted
	time.Sleep(150 * time.Millisecond) // let the await reach its blocking wait

	if resp, err := http.Post(base+"/api/shutdown", "application/json", nil); err != nil {
		t.Fatalf("POST /api/shutdown: %v", err)
	} else {
		resp.Body.Close()
	}

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned %v, want nil", err)
		}
	case <-time.After(shutdownDrainBudget + 2*time.Second):
		t.Fatal("held long-poll stalled shutdown past the drain budget")
	}
}

// TestShutdownMethodDiscipline (spec: Method discipline): GET is refused with
// 405 and the daemon keeps serving.
func TestShutdownMethodDiscipline(t *testing.T) {
	base, errCh := startRun(t)

	resp, err := http.Get(base + "/api/shutdown")
	if err != nil {
		t.Fatalf("GET /api/shutdown: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /api/shutdown = %d, want 405", resp.StatusCode)
	}

	// Still alive: /health answers and Run has not returned.
	h, err := http.Get(base + "/health")
	if err != nil {
		t.Fatalf("daemon stopped serving after a refused GET: %v", err)
	}
	h.Body.Close()
	select {
	case err := <-errCh:
		t.Fatalf("Run returned (%v) after a refused GET /api/shutdown", err)
	default:
	}

	// Clean up the daemon so the test leaves no listener behind.
	if resp, err := http.Post(base+"/api/shutdown", "application/json", nil); err == nil {
		resp.Body.Close()
	}
	select {
	case <-errCh:
	case <-time.After(shutdownDrainBudget + 2*time.Second):
		t.Fatal("cleanup shutdown did not complete")
	}
}

// TestBoardAssetsNeverCallShutdown enforces the read-only Case Board invariant
// (case-board-ui design D2 + daemon-self-heal-on-upgrade spec): no embedded
// board asset may reference the shutdown endpoint.
func TestBoardAssetsNeverCallShutdown(t *testing.T) {
	root := filepath.Join("ui", "assets")
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), "/api/shutdown") {
			t.Errorf("board asset %s references /api/shutdown — the Case Board must stay read-only", path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}
