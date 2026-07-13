package daemon

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// TestServeWithoutWatcherStillServes covers the disabled-watcher branch (D-B): when
// the executable can't be resolved/stat'd at startup the watcher is nil, and serve
// must keep the pre-watcher behavior — serve normally, never drain on its own. The
// daemon then only stops when its listener goes away (a manual kill in production).
func TestServeWithoutWatcherStillServes(t *testing.T) {
	srv, _ := newTestServerAt(t, t.TempDir())

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	done := make(chan error, 1)
	go func() { done <- serve(srv, ln, nil) }()

	// It must serve normally with no watcher attached.
	resp, err := http.Get("http://" + ln.Addr().String() + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("/health status = %d, want 200", resp.StatusCode)
	}

	// It must not have drained on its own; serve stays blocked until the listener
	// closes (the pre-watcher lifecycle).
	select {
	case err := <-done:
		t.Fatalf("serve returned early without a watcher: %v", err)
	case <-time.After(120 * time.Millisecond):
	}

	// Closing the listener ends serve (as a manual kill would in production).
	ln.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("serve did not return after the listener closed")
	}
}

// TestRestartSwapPreservesInvestigation is the swap integration test (restart-on-
// upgrade 3.1, daemon-lifecycle spec: "Upgrade mid-investigation"). It stands the
// full serve+watch+drain path up over an ephemeral listener and a temp store root,
// with a session holding an OPEN run; a synthetic binary replacement triggers the
// watcher; the daemon drains and exits cleanly (exit 0 via a nil serve return); and
// a fresh Server over the same root proves the session and its open run replayed and
// that await_run re-attaches to that run. No investigation data is lost across the
// swap.
func TestRestartSwapPreservesInvestigation(t *testing.T) {
	root := t.TempDir()

	// A case mid-investigation: a session with an open run, created through the
	// first daemon's store (which persists synchronously, so a restart loses nothing).
	srv, st := newTestServerAt(t, root)
	sess := mustSession(t, st)
	openRun, err := st.OpenOrAttachRun(sess.ID)
	if err != nil {
		t.Fatalf("OpenOrAttachRun: %v", err)
	}

	// A temp file standing in for the daemon's own executable; capture its baseline
	// before serving, exactly as Run does before the listener opens (D-B). We never
	// watch the real test binary.
	exe := filepath.Join(t.TempDir(), "sherlog")
	if err := os.WriteFile(exe, []byte("v1"), 0o755); err != nil {
		t.Fatalf("write exe: %v", err)
	}
	baseline, err := captureBinIdentity(exe)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	// Ephemeral loopback listener — never assume port 2218 is free.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	watcher := &binWatcher{
		path:     exe,
		interval: 5 * time.Millisecond,
		baseline: baseline,
		maxDrain: time.Minute,
		inFlight: func() int64 { return srv.awaiter.inFlight.Load() },
		logf:     func(string, ...any) {},
	}

	serveDone := make(chan error, 1)
	go func() { serveDone <- serve(srv, ln, watcher) }()

	// Synthetic upgrade: replace the "binary" on disk (rename-over, the atomic
	// install pattern used by both brew and go install).
	newer := filepath.Join(t.TempDir(), "sherlog.new")
	if err := os.WriteFile(newer, []byte("v2-longer"), 0o755); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if err := os.Rename(newer, exe); err != nil {
		t.Fatalf("rename-over: %v", err)
	}

	// The daemon drains (gauge zero: nothing is awaiting) and exits cleanly.
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve returned an error, want a clean drained exit: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("daemon did not drain and exit after the binary was replaced")
	}

	// A fresh Server over the SAME root is the respawned new binary: the session and
	// its open run must replay intact.
	fresh, freshStore := newTestServerAt(t, root)

	sess2, err := freshStore.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("replayed GetSession: %v", err)
	}
	var replayedOpen *store.Run
	for i := range sess2.Runs {
		if sess2.Runs[i].ClosedAt == nil {
			replayedOpen = &sess2.Runs[i]
		}
	}
	if replayedOpen == nil {
		t.Fatal("no open run replayed after the restart — investigation state lost")
	}
	if replayedOpen.ID != openRun.ID {
		t.Errorf("replayed open run = %q, want the original %q", replayedOpen.ID, openRun.ID)
	}

	// await_run re-attaches to that same open run (D8), so the investigation
	// continues off replayed state with no user action.
	res, err := fresh.awaiter.await(context.Background(), sess.ID, 200*time.Millisecond, "")
	if err != nil {
		t.Fatalf("await after restart: %v", err)
	}
	if res.Run.ID != openRun.ID {
		t.Errorf("await re-attached to run %q, want the replayed open run %q", res.Run.ID, openRun.ID)
	}
}
