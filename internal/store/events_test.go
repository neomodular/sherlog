package store

import (
	"sync"
	"testing"
	"time"
)

// recvEvent waits for one event on a subscription, failing the test on timeout so
// a missing publish surfaces as a clear failure rather than a hang.
func recvEvent(t *testing.T, sub Subscription) Event {
	t.Helper()
	select {
	case ev, ok := <-sub.C:
		if !ok {
			t.Fatalf("subscription channel closed unexpectedly")
		}
		return ev
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for event")
		return Event{}
	}
}

// TestSubscribeReceivesAllKinds verifies each mutation publishes its typed event:
// board (set + update), probe (register + remove), run (open + close), log (ingest).
func TestSubscribeReceivesAllKinds(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("events", "/repo")

	sub := s.Subscribe()
	defer sub.Unsubscribe()

	type step struct {
		do   func()
		kind EventKind
	}
	steps := []step{
		{func() { s.SetHypotheses(sess.ID, []string{"a", "b"}) }, EventBoard},
		{func() { s.UpdateHypothesis(sess.ID, "h1", HypothesisKilled, "n") }, EventBoard},
		{func() { s.RegisterProbe(sess.ID, Probe{ID: "p1", File: "f", Line: 1}) }, EventProbe},
		{func() { s.OpenRun(sess.ID) }, EventRun},
		{func() { s.Ingest(sess.ID, "p1", nil, "x") }, EventLog},
		{func() { s.CloseLatestOpenRun(sess.ID, VerdictReproduced) }, EventRun},
		{func() { s.RemoveProbe(sess.ID, "p1") }, EventProbe},
	}
	for _, st := range steps {
		st.do()
		ev := recvEvent(t, sub)
		if ev.Kind != st.kind {
			t.Errorf("expected kind %q, got %q (%+v)", st.kind, ev.Kind, ev)
		}
		if ev.Session != sess.ID {
			t.Errorf("event session %q want %q", ev.Session, sess.ID)
		}
		if ev.Payload == nil {
			t.Errorf("event %q carried no payload", ev.Kind)
		}
	}
}

// TestCloseSessionPublishesRunEvent verifies closing a session emits a run event
// (the case left the open set) exactly once — the idempotent re-close is silent.
func TestCloseSessionPublishesRunEvent(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("close pub", "/repo")
	sub := s.Subscribe()
	defer sub.Unsubscribe()

	s.CloseSession(sess.ID)
	ev := recvEvent(t, sub)
	if ev.Kind != EventRun || ev.Session != sess.ID {
		t.Errorf("close should publish a run event for the session: %+v", ev)
	}

	// Re-close is idempotent and must not publish again.
	s.CloseSession(sess.ID)
	select {
	case ev := <-sub.C:
		t.Errorf("idempotent re-close should not publish, got %+v", ev)
	case <-time.After(100 * time.Millisecond):
	}
}

// TestStalledSubscriberDropped covers case-board-ui scenario "Slow browser does
// not block the daemon": a subscriber that stops reading is dropped (its channel
// closed) once its buffer fills, without blocking the publisher, while a healthy
// subscriber keeps receiving.
func TestStalledSubscriberDropped(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("stall", "/repo")

	slow := s.Subscribe() // never drained
	fast := s.Subscribe()
	defer fast.Unsubscribe()

	// Publish more than the buffer can hold so the slow subscriber overflows and is
	// dropped. The publisher must not block: the whole loop completes promptly.
	done := make(chan struct{})
	go func() {
		for i := 0; i < subBufferN*3; i++ {
			s.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, "")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("publisher blocked on a stalled subscriber")
	}

	// The slow subscriber's channel must eventually be observed closed (drained then
	// closed), proving it was dropped rather than left to block forever.
	drainedAndClosed := false
	deadline := time.After(2 * time.Second)
	for !drainedAndClosed {
		select {
		case _, ok := <-slow.C:
			if !ok {
				drainedAndClosed = true
			}
		case <-deadline:
			t.Fatal("stalled subscriber was never dropped/closed")
		}
	}

	// The fast subscriber still works after the drop.
	s.Ingest(sess.ID, "p1", nil, "after")
	if ev := recvEvent(t, fast); ev.Kind != EventLog {
		t.Errorf("healthy subscriber should still receive: %+v", ev)
	}
}

// TestUnsubscribeIdempotentAndStops verifies Unsubscribe closes the channel, stops
// further delivery, and is safe to call twice (no double-close panic).
func TestUnsubscribeIdempotentAndStops(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("unsub", "/repo")
	sub := s.Subscribe()

	sub.Unsubscribe()
	sub.Unsubscribe() // must not panic

	// Channel is closed; receive returns the zero value with ok=false.
	if _, ok := <-sub.C; ok {
		t.Errorf("channel should be closed after Unsubscribe")
	}

	// A publish after unsubscribe must not panic (no send on a closed channel) and
	// must not be observable.
	s.Ingest(sess.ID, "p1", nil, "x")
	if _, ok := <-sub.C; ok {
		t.Errorf("no delivery after unsubscribe")
	}
}

// TestConcurrentPublishAndSubscribe stresses pub/sub under concurrent publishers,
// subscribers, and unsubscribes to surface races under -race and confirm no
// publisher ever blocks or panics. Subscribers drain continuously so none stall.
func TestConcurrentPublishAndSubscribe(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("concurrent pub", "/repo")
	s.OpenRun(sess.ID)

	var wg sync.WaitGroup

	// Continuously churning subscribers: subscribe, drain briefly, unsubscribe.
	for c := 0; c < 8; c++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				sub := s.Subscribe()
				go func() {
					for range sub.C { // drain until closed
					}
				}()
				time.Sleep(time.Millisecond)
				sub.Unsubscribe()
			}
		}()
	}

	// Concurrent publishers across event kinds.
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 100; i++ {
				s.Ingest(sess.ID, "p1", map[string]any{"i": float64(i)}, "")
			}
		}()
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("concurrent pub/sub deadlocked")
	}
}
