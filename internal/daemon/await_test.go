package daemon

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// TestAwaitInFlightGaugeCountsConcurrent verifies the drain gauge (D-C) counts each
// blocking await for the full life of the call and returns to zero once they all
// resolve: concurrent awaits must be counted correctly so the watcher never exits
// while a reproduction wait is mid-flight.
func TestAwaitInFlightGaugeCountsConcurrent(t *testing.T) {
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	engine := newAwaitEngine(st, 50*time.Millisecond, 10*time.Second)

	const n = 5
	// Distinct cwds yield distinct sessions (the store dedups sessions by cwd).
	sessions := make([]string, n)
	for i := range sessions {
		sess, _, err := st.CreateSession("", "bug", fmt.Sprintf("/tmp/app-%d", i))
		if err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		sessions[i] = sess.ID
	}

	if got := engine.inFlight.Load(); got != 0 {
		t.Fatalf("gauge before any await = %d, want 0", got)
	}

	var wg sync.WaitGroup
	wg.Add(n)
	for _, id := range sessions {
		go func(sessionID string) {
			defer wg.Done()
			// No activity arrives, so each await blocks the full timeout window; that
			// keeps all n calls in flight simultaneously for the gauge observation.
			if _, err := engine.await(context.Background(), sessionID, 400*time.Millisecond, ""); err != nil {
				t.Errorf("await(%s): %v", sessionID, err)
			}
		}(id)
	}

	// The gauge must reach exactly n while all awaits are blocking.
	deadline := time.Now().Add(2 * time.Second)
	for engine.inFlight.Load() != n {
		if time.Now().After(deadline) {
			t.Fatalf("gauge never reached %d (last read %d)", n, engine.inFlight.Load())
		}
		time.Sleep(5 * time.Millisecond)
	}

	wg.Wait()
	if got := engine.inFlight.Load(); got != 0 {
		t.Errorf("gauge after all awaits returned = %d, want 0", got)
	}
}
