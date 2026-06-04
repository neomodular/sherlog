package daemon

import (
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

// freeLoopbackPort binds an ephemeral loopback port, closes it, and returns the
// number so a caller can attempt to claim it. Inherently racy in the abstract,
// but adequate for a single-process test on the loopback interface.
func freeLoopbackPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for free port: %v", err)
	}
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("SplitHostPort: %v", err)
	}
	ln.Close()
	return port
}

// TestBindFailureInUse verifies the actionable error path when the port is held
// by another listener: isAddrInUse must detect it (cross-platform, incl. the
// Windows WSAEADDRINUSE 10048 branch) and bindError must name the port and point
// at SHERLOG_PORT.
func TestBindFailureInUse(t *testing.T) {
	port := freeLoopbackPort(t)
	addr := net.JoinHostPort("127.0.0.1", port)

	held, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("hold port %s: %v", addr, err)
	}
	defer held.Close()

	// Second listen on the same addr must fail with an in-use condition.
	_, err = net.Listen("tcp", addr)
	if err == nil {
		t.Fatal("second listen unexpectedly succeeded")
	}
	if !isAddrInUse(err) {
		t.Fatalf("isAddrInUse(%v) = false, want true", err)
	}

	msg := bindError(addr, port, err).Error()
	if !strings.Contains(msg, port) {
		t.Errorf("bind error %q does not mention port %q", msg, port)
	}
	if !strings.Contains(msg, "in use") {
		t.Errorf("bind error %q does not state the port is in use", msg)
	}
	if !strings.Contains(msg, "SHERLOG_PORT") {
		t.Errorf("bind error %q does not mention SHERLOG_PORT guidance", msg)
	}
}

// TestBindErrorOtherIsNotInUse confirms a non-EADDRINUSE failure is not
// misreported as an in-use condition and yields the generic message.
func TestBindErrorOtherIsNotInUse(t *testing.T) {
	// A bind to a port we lack permission/route for surfaces a non-EADDRINUSE
	// error; an invalid address reliably does so without needing privileges.
	_, err := net.Listen("tcp", "127.0.0.1:999999")
	if err == nil {
		t.Skip("invalid port unexpectedly accepted on this platform")
	}
	if isAddrInUse(err) {
		t.Errorf("isAddrInUse(%v) = true, want false for a non-in-use error", err)
	}
	msg := bindError("127.0.0.1:999999", "999999", err).Error()
	if strings.Contains(msg, "SHERLOG_PORT") {
		t.Errorf("generic bind error %q should not carry SHERLOG_PORT guidance", msg)
	}
}

// TestRunHonorsSHERLOGPortFree starts the daemon on a SHERLOG_PORT-selected free
// port and confirms it comes up by polling /health on that exact port.
func TestRunHonorsSHERLOGPortFree(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir()) // Windows home resolution
	port := freeLoopbackPort(t)
	t.Setenv("SHERLOG_PORT", port)

	// Run blocks on Serve once bound; drive it from a goroutine and report any
	// early (bind) failure back so the test fails loudly instead of hanging.
	errCh := make(chan error, 1)
	go func() { errCh <- Run("test") }()

	url := "http://" + net.JoinHostPort("127.0.0.1", port) + "/health"
	deadline := time.Now().Add(3 * time.Second)
	for {
		select {
		case err := <-errCh:
			t.Fatalf("Run returned early on a free port: %v", err)
		default:
		}
		resp, err := http.Get(url)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return // daemon came up on the SHERLOG_PORT-selected port
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("daemon did not answer /health on port %s within deadline", port)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// TestRunHonorsSHERLOGPortHeld confirms SHERLOG_PORT pointing at a held port
// makes Run fail fast with the actionable in-use error rather than block.
func TestRunHonorsSHERLOGPortHeld(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("USERPROFILE", t.TempDir())
	port := freeLoopbackPort(t)
	held, err := net.Listen("tcp", net.JoinHostPort("127.0.0.1", port))
	if err != nil {
		t.Fatalf("hold port: %v", err)
	}
	defer held.Close()

	t.Setenv("SHERLOG_PORT", port)

	errCh := make(chan error, 1)
	go func() { errCh <- Run("test") }()

	select {
	case err := <-errCh:
		if err == nil {
			t.Fatal("Run succeeded on a held port, want bind failure")
		}
		msg := err.Error()
		if !strings.Contains(msg, port) || !strings.Contains(msg, "SHERLOG_PORT") {
			t.Errorf("bind failure %q lacks port or SHERLOG_PORT guidance", msg)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Run blocked instead of failing fast on a held port")
	}
}
