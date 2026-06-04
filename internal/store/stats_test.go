package store

import (
	"testing"
	"time"
)

// TestStatsCountersUnderIngest covers the store activity snapshot (add-health-page
// D2): ingest bumps the total, last-event time, and the trailing-hour count, and the
// session counts reflect open vs closed.
func TestStatsCountersUnderIngest(t *testing.T) {
	st, err := New(WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, _, err := st.CreateSession("bug", "/tmp/app")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	now := time.Now()
	if a := st.Stats(now); a.TotalEvents != 0 || a.LastEvent != nil || a.HourlyEvents != 0 {
		t.Fatalf("pre-ingest stats nonzero: %+v", a)
	}

	const fired = 5
	for i := 0; i < fired; i++ {
		if err := st.Ingest(sess.ID, "p1", map[string]int{"i": i}, ""); err != nil {
			t.Fatalf("Ingest: %v", err)
		}
	}

	a := st.Stats(time.Now())
	if a.TotalEvents != fired {
		t.Errorf("TotalEvents = %d, want %d", a.TotalEvents, fired)
	}
	if a.HourlyEvents != fired {
		t.Errorf("HourlyEvents = %d, want %d", a.HourlyEvents, fired)
	}
	if a.LastEvent == nil {
		t.Error("LastEvent nil after ingest")
	}
	if a.OpenSessions != 1 || a.ClosedSessions != 0 {
		t.Errorf("session counts = open %d closed %d, want 1/0", a.OpenSessions, a.ClosedSessions)
	}

	if _, err := st.CloseSession(sess.ID); err != nil {
		t.Fatalf("CloseSession: %v", err)
	}
	if a := st.Stats(time.Now()); a.OpenSessions != 0 || a.ClosedSessions != 1 {
		t.Errorf("after close, session counts = open %d closed %d, want 0/1", a.OpenSessions, a.ClosedSessions)
	}
}

// TestHourlyWindowExpires covers the 60-bucket ring's trailing-hour semantics: events
// older than an hour fall out of the hourly count while the lifetime total stands.
func TestHourlyWindowExpires(t *testing.T) {
	var c ingestCounters

	old := time.Now().Add(-2 * time.Hour)
	c.recordIngest(old)
	recent := time.Now()
	c.recordIngest(recent)

	if c.totalEvents != 2 {
		t.Fatalf("totalEvents = %d, want 2", c.totalEvents)
	}
	if got := c.hourlyTotal(recent); got != 1 {
		t.Errorf("hourlyTotal = %d, want 1 (the 2h-old event is outside the window)", got)
	}
}

// TestHourlyRingReuseSlot covers the wrap-around guard: two events exactly an hour
// apart map to the same ring slot, and the stale slot is reset rather than summed, so
// the count never double-counts a wrapped minute.
func TestHourlyRingReuseSlot(t *testing.T) {
	var c ingestCounters

	base := time.Unix(0, 0)
	c.recordIngest(base)                                          // slot for minute 0
	later := base.Add(time.Duration(hourlyBuckets) * time.Minute) // same slot, 60 min later
	c.recordIngest(later)

	if got := c.hourlyTotal(later); got != 1 {
		t.Errorf("hourlyTotal at wrap = %d, want 1 (stale slot reset, not summed)", got)
	}
}

// TestStatsOpenRunSelection covers OpenRun pointing at the most recently created open
// session that holds an open run (add-health-page).
func TestStatsOpenRunSelection(t *testing.T) {
	st, err := New(WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	older, _, _ := st.CreateSession("older", "/a")
	newer, _, _ := st.CreateSession("newer", "/b")
	if _, err := st.OpenRun(older.ID); err != nil {
		t.Fatalf("OpenRun older: %v", err)
	}
	run, err := st.OpenRun(newer.ID)
	if err != nil {
		t.Fatalf("OpenRun newer: %v", err)
	}

	a := st.Stats(time.Now())
	if a.OpenRun == nil || a.OpenRun.Session != newer.ID || a.OpenRun.Run != run.ID {
		t.Errorf("OpenRun = %+v, want newest session %q run %q", a.OpenRun, newer.ID, run.ID)
	}
}
