package daemon

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

// TestSameBinary is the table-driven comparison helper test (restart-on-upgrade
// 1.1): replaced, deleted, untouched, and touch-only, across both the syscall
// (dev/inode) view and the portable mtime+size fallback.
func TestSameBinary(t *testing.T) {
	t0 := time.Unix(1_700_000_000, 0)
	t1 := t0.Add(time.Minute)

	base := binIdentity{exists: true, hasSys: true, dev: 1, ino: 42, size: 100, mtime: t0}

	tests := []struct {
		name string
		cur  binIdentity
		want bool
	}{
		{"untouched", base, true},
		{"replaced_inode", binIdentity{exists: true, hasSys: true, dev: 1, ino: 43, size: 100, mtime: t0}, false},
		{"replaced_device", binIdentity{exists: true, hasSys: true, dev: 2, ino: 42, size: 100, mtime: t0}, false},
		{"deleted", binIdentity{exists: false}, false},
		{"touch_only_mtime", binIdentity{exists: true, hasSys: true, dev: 1, ino: 42, size: 100, mtime: t1}, false},
		{"size_changed", binIdentity{exists: true, hasSys: true, dev: 1, ino: 42, size: 200, mtime: t0}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameBinary(base, tc.cur); got != tc.want {
				t.Errorf("sameBinary(base, %s) = %v, want %v", tc.cur, got, tc.want)
			}
		})
	}

	// Portable fallback: hasSys=false on both sides falls back to mtime+size only.
	t.Run("fallback", func(t *testing.T) {
		fbase := binIdentity{exists: true, hasSys: false, size: 100, mtime: t0}
		cases := []struct {
			name string
			cur  binIdentity
			want bool
		}{
			{"untouched", binIdentity{exists: true, size: 100, mtime: t0}, true},
			{"replaced_size", binIdentity{exists: true, size: 101, mtime: t0}, false},
			{"replaced_mtime", binIdentity{exists: true, size: 100, mtime: t1}, false},
			{"deleted", binIdentity{exists: false}, false},
		}
		for _, tc := range cases {
			if got := sameBinary(fbase, tc.cur); got != tc.want {
				t.Errorf("%s: sameBinary(fallback) = %v, want %v", tc.name, got, tc.want)
			}
		}
	})

	// Both-absent is a degenerate equality (a binary that was already gone at
	// startup stays gone) — it must not spuriously trigger.
	t.Run("both_absent", func(t *testing.T) {
		if !sameBinary(binIdentity{exists: false}, binIdentity{exists: false}) {
			t.Error("sameBinary(absent, absent) = false, want true")
		}
	})
}

// TestCaptureBinIdentity confirms a present file yields an identity and a missing
// file is a valid absent identity rather than an error (the watcher reads absence
// as a delete trigger, D-A).
func TestCaptureBinIdentity(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("hello"), 0o755); err != nil {
		t.Fatalf("write temp bin: %v", err)
	}

	id, err := captureBinIdentity(path)
	if err != nil {
		t.Fatalf("captureBinIdentity(present) error: %v", err)
	}
	if !id.exists || id.size != 5 {
		t.Errorf("present identity = %+v, want exists with size 5", id)
	}

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove temp bin: %v", err)
	}
	absent, err := captureBinIdentity(path)
	if err != nil {
		t.Fatalf("captureBinIdentity(absent) unexpected error: %v", err)
	}
	if absent.exists {
		t.Errorf("absent identity = %+v, want exists=false", absent)
	}
}

// newTestWatcher builds a watcher with a fast interval, a captured (silent) logger,
// and the given in-flight gauge so tests never watch the real test binary and never
// leak a goroutine.
func newTestWatcher(path string, baseline binIdentity, maxDrain time.Duration, inFlight func() int64) *binWatcher {
	return &binWatcher{
		path:     path,
		interval: 5 * time.Millisecond,
		baseline: baseline,
		maxDrain: maxDrain,
		inFlight: inFlight,
		logf:     func(string, ...any) {}, // silence the drain line in tests
	}
}

// TestWatcherReplacedTriggers replaces the watched file (rename-over, the atomic
// install pattern) and expects a drain-to-exit within a few intervals.
func TestWatcherReplacedTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("v1"), 0o755); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	baseline, err := captureBinIdentity(path)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	w := newTestWatcher(path, baseline, time.Minute, func() int64 { return 0 })
	stop := make(chan struct{})
	defer close(stop)
	done := make(chan bool, 1)
	go func() { done <- w.run(stop) }()

	// Rename a differently-sized new file over the path so both the syscall (new
	// inode) and fallback (new size) views observe the change.
	newer := filepath.Join(dir, "bin.new")
	if err := os.WriteFile(newer, []byte("v2-longer"), 0o755); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	if err := os.Rename(newer, path); err != nil {
		t.Fatalf("rename-over: %v", err)
	}

	select {
	case got := <-done:
		if !got {
			t.Fatal("watcher returned false (stopped) instead of draining to exit")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not drain to exit after a rename-over")
	}
}

// TestWatcherDeletedTriggers removes the watched file (as brew cleanup does to the
// old Cellar path) and expects a drain-to-exit.
func TestWatcherDeletedTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("v1"), 0o755); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	baseline, err := captureBinIdentity(path)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	w := newTestWatcher(path, baseline, time.Minute, func() int64 { return 0 })
	stop := make(chan struct{})
	defer close(stop)
	done := make(chan bool, 1)
	go func() { done <- w.run(stop) }()

	if err := os.Remove(path); err != nil {
		t.Fatalf("remove: %v", err)
	}

	select {
	case got := <-done:
		if !got {
			t.Fatal("watcher returned false instead of draining to exit on delete")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not drain to exit after a delete")
	}
}

// TestWatcherUntouchedNeverTriggers leaves the file alone: the watcher must keep
// running until stopped, never draining.
func TestWatcherUntouchedNeverTriggers(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("stable"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	baseline, err := captureBinIdentity(path)
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}

	w := newTestWatcher(path, baseline, time.Minute, func() int64 { return 0 })
	stop := make(chan struct{})
	done := make(chan bool, 1)
	go func() { done <- w.run(stop) }()

	// Give it plenty of ticks; it must not drain.
	select {
	case got := <-done:
		t.Fatalf("watcher exited early (got=%v) on an untouched binary", got)
	case <-time.After(150 * time.Millisecond):
	}

	close(stop)
	select {
	case got := <-done:
		if got {
			t.Error("watcher returned true (drained) after stop, want false")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not return after stop")
	}
}

// triggeringWatcher builds a watcher whose baseline is guaranteed to differ from a
// real present file, so it triggers on the first tick — isolating the drain
// behavior (D-C) from file-mutation timing.
func triggeringWatcher(t *testing.T, maxDrain time.Duration, inFlight func() int64) *binWatcher {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "bin")
	if err := os.WriteFile(path, []byte("on-disk"), 0o755); err != nil {
		t.Fatalf("write: %v", err)
	}
	// A synthetic "old" baseline that no present file can match.
	stale := binIdentity{exists: true, hasSys: false, size: 1, mtime: time.Unix(1, 0)}
	return newTestWatcher(path, stale, maxDrain, inFlight)
}

// TestWatcherDrainWaitsForInFlight verifies the in-flight await completes first:
// with the gauge non-zero the watcher stays draining, and it exits only once the
// gauge falls to zero (D-C: "exits only after that await returns").
func TestWatcherDrainWaitsForInFlight(t *testing.T) {
	var gauge atomic.Int64
	gauge.Store(1) // one await blocking
	w := triggeringWatcher(t, time.Minute, gauge.Load)

	stop := make(chan struct{})
	defer close(stop)
	done := make(chan bool, 1)
	go func() { done <- w.run(stop) }()

	// It must not exit while an await is in flight.
	select {
	case <-done:
		t.Fatal("watcher exited while an await was still in flight")
	case <-time.After(120 * time.Millisecond):
	}

	gauge.Store(0) // the await returned to its caller
	select {
	case got := <-done:
		if !got {
			t.Fatal("watcher returned false, want a drain-to-exit once the gauge hit zero")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not exit after the in-flight await drained")
	}
}

// TestWatcherDrainFallback verifies the bounded fallback: a wedged await (gauge
// stuck non-zero) does not pin the stale binary forever — the watcher exits once
// the drain exceeds maxDrain (D-C).
func TestWatcherDrainFallback(t *testing.T) {
	const maxDrain = 60 * time.Millisecond
	w := triggeringWatcher(t, maxDrain, func() int64 { return 1 }) // never drains to zero

	stop := make(chan struct{})
	defer close(stop)
	done := make(chan bool, 1)
	start := time.Now()
	go func() { done <- w.run(stop) }()

	select {
	case got := <-done:
		if !got {
			t.Fatal("watcher returned false, want a fallback drain-to-exit")
		}
		if elapsed := time.Since(start); elapsed < maxDrain {
			t.Errorf("fallback fired after %v, want >= maxDrain %v", elapsed, maxDrain)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bounded fallback never fired with a wedged await")
	}
}

// TestWatcherNewAwaitDuringDrainServed verifies awaits arriving during the drain
// are still served (D-C): while the gauge fluctuates above zero the watcher keeps
// waiting, exiting only when it finally reaches zero.
func TestWatcherNewAwaitDuringDrainServed(t *testing.T) {
	var gauge atomic.Int64
	gauge.Store(1)
	w := triggeringWatcher(t, time.Minute, gauge.Load)

	stop := make(chan struct{})
	defer close(stop)
	done := make(chan bool, 1)
	go func() { done <- w.run(stop) }()

	// Simulate a new await arriving mid-drain (gauge 1 -> 2) then the first one
	// finishing (2 -> 1): the count never hits zero, so the watcher must not exit.
	time.Sleep(40 * time.Millisecond)
	gauge.Store(2)
	time.Sleep(40 * time.Millisecond)
	gauge.Store(1)

	select {
	case <-done:
		t.Fatal("watcher exited while awaits were still being served during drain")
	case <-time.After(60 * time.Millisecond):
	}

	// All awaits drain: the watcher exits.
	gauge.Store(0)
	select {
	case got := <-done:
		if !got {
			t.Fatal("watcher returned false, want exit once all drained awaits cleared")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watcher did not exit after the drain completed")
	}
}
