package daemon

import (
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"time"
)

// binaryWatchInterval is how often the daemon re-stats its own executable to
// detect a landed upgrade (restart-on-upgrade D-B). Fixed at 30s following the
// retention-pruning ticker precedent; deliberately not configurable (YAGNI until
// field data says otherwise, D-B).
const binaryWatchInterval = 30 * time.Second

// binIdentity is the on-disk identity of the daemon's executable used to detect
// replacement (D-A). device+inode uniquely identify the file across a rename-over
// install; mtime+size are the portable fallback where the syscall view is
// unavailable (non-unix) and also catch a touch/re-sign — a spurious restart there
// is harmless by design (state survives, the next tool call respawns). exists
// distinguishes a present file from a vanished one (brew cleanup deleting the old
// Cellar path), which is itself a trigger.
type binIdentity struct {
	exists bool      // whether the file was present at capture
	hasSys bool      // whether dev/ino came from the platform stat view
	dev    uint64    // device number; populated only when hasSys
	ino    uint64    // inode number; populated only when hasSys
	size   int64     // file size in bytes
	mtime  time.Time // modification time
}

// String renders an identity for the drain log line so operators can see exactly
// what changed (D-D: one line naming old vs observed). A vanished file reads as
// "absent"; a present file names inode/size/mtime (or just size/mtime on the
// portable fallback).
func (id binIdentity) String() string {
	if !id.exists {
		return "absent"
	}
	if id.hasSys {
		return fmt.Sprintf("dev=%d ino=%d size=%d mtime=%s", id.dev, id.ino, id.size, id.mtime.Format(time.RFC3339Nano))
	}
	return fmt.Sprintf("size=%d mtime=%s", id.size, id.mtime.Format(time.RFC3339Nano))
}

// captureBinIdentity stats path and records its identity. A non-existent file is
// not an error here — it is a valid identity (exists=false) that the watcher reads
// as "the binary was replaced/removed". Any other stat failure (permission, I/O)
// is returned wrapped so the caller can distinguish: at startup it disables the
// watcher; on a tick it treats the failure as transient and retries next tick.
func captureBinIdentity(path string) (binIdentity, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return binIdentity{exists: false}, nil
		}
		return binIdentity{}, fmt.Errorf("stat executable %q: %w", path, err)
	}
	id := binIdentity{
		exists: true,
		size:   info.Size(),
		mtime:  info.ModTime(),
	}
	if dev, ino, ok := sysIdentity(info); ok {
		id.dev, id.ino, id.hasSys = dev, ino, true
	}
	return id, nil
}

// sameBinary reports whether cur is the same executable as base (D-A). Identity is
// device+inode+mtime+size when the syscall view is present on both; otherwise the
// portable mtime+size comparison. A file present in one and absent in the other is
// never the same. An mtime or size change (a touch or re-sign) counts as a change:
// a spurious restart is harmless by design — state survives, the next tool call
// respawns off the same disk state.
func sameBinary(base, cur binIdentity) bool {
	if base.exists != cur.exists {
		return false
	}
	if !base.exists { // both absent — nothing to compare
		return true
	}
	if base.hasSys && cur.hasSys && (base.dev != cur.dev || base.ino != cur.ino) {
		return false
	}
	return base.size == cur.size && base.mtime.Equal(cur.mtime)
}

// binWatcher watches the daemon's own executable and, once a replacement is
// observed, drains and signals a clean exit (restart-on-upgrade D-B..D-D). Every
// knob is injectable so tests drive it synthetically — a temp file as the path, a
// short interval, a fake in-flight gauge, a captured logger — and never watch the
// real test binary or leak a goroutine.
type binWatcher struct {
	path     string        // executable path to watch (os.Executable() in production)
	interval time.Duration // re-stat cadence (binaryWatchInterval in production)
	baseline binIdentity   // identity captured before the listener opened (D-B)
	maxDrain time.Duration // bounded drain fallback = await_max_timeout (D-C)
	inFlight func() int64  // reads the await in-flight gauge (D-C)
	logf     func(format string, args ...any)
}

// logline routes a watcher log line to the injected logger, or log.Printf (which
// writes to stderr — visible in nohup/launchd logs, D-D) when none is injected.
func (w *binWatcher) logline(format string, args ...any) {
	if w.logf != nil {
		w.logf(format, args...)
		return
	}
	log.Printf(format, args...)
}

// run watches the executable on the ticker and returns true once a replacement is
// observed and the daemon has drained toward exit; it returns false if stop is
// closed first (the server exited on its own, or a test tore the watcher down).
//
// Drain semantics (D-C): on the first tick where the identity differs, the watcher
// marks itself draining, logs one line naming old vs observed, and exits on that
// same tick if no await is in flight. If an await is still blocking it keeps
// ticking — new awaits arriving during the drain are still served — and exits on
// the first tick the gauge reads zero, or once the bounded fallback (maxDrain) has
// elapsed, whichever comes first. A wedged await must never pin a stale binary
// forever.
func (w *binWatcher) run(stop <-chan struct{}) bool {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()

	draining := false
	var drainDeadline time.Time
	for {
		select {
		case <-stop:
			return false
		case now := <-ticker.C:
			if !draining {
				cur, err := captureBinIdentity(w.path)
				if err != nil {
					// A transient stat failure (not a vanished file — that is a valid
					// absent identity) must never trigger a restart: skip this tick and
					// retry on the next one.
					w.logline("sherlog: binary watch stat error (ignored): %v", err)
					continue
				}
				if sameBinary(w.baseline, cur) {
					continue // unchanged — keep running indefinitely
				}
				draining = true
				drainDeadline = now.Add(w.maxDrain)
				w.logline("sherlog: daemon executable changed (was %s, now %s); draining before restart", w.baseline, cur)
			}
			// Draining: exit on the first tick with the await gauge at zero, or once the
			// bounded fallback deadline has passed (D-C).
			if w.inFlight() == 0 || !now.Before(drainDeadline) {
				return true
			}
		}
	}
}
