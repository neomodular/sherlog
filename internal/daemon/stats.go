package daemon

import (
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// diskCacheTTL bounds how stale the data-directory size walk may be (add-health-page
// D2): the health view polls /api/stats every ~5s, so a 5s cache means at most one
// walk per poll cycle even with several tabs open.
const diskCacheTTL = 5 * time.Second

// diskUsageCache memoizes the data-directory size walk for diskCacheTTL so repeated
// /api/stats polls do not re-walk ~/.sherlog (add-health-page D2). It is guarded by
// its own mutex, independent of the store and the subscriber gauge, since the walk
// touches only the filesystem.
type diskUsageCache struct {
	mu       sync.Mutex
	bytes    int64
	computed time.Time // zero until the first walk
}

// SetBindHost records the host the daemon's listener actually bound to so the
// loopback_only self-check reflects reality rather than the 127.0.0.1 default
// (add-health-page D3). Run calls this once after Listen; an empty host is ignored
// so the safe default stands.
func (s *Server) SetBindHost(host string) {
	if host != "" {
		s.bindHost = host
	}
}

// SelfCheck is one boolean health probe with a human-readable reason (add-health-page
// D3): the view's status header derives from the set — all ok → mascot + "on the
// case"; any failure → the failing check's detail. Detail explains the state in both
// the passing and failing case so the panel always has something to show.
type SelfCheck struct {
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Vitals are the daemon's process facts for the health view (add-health-page D2).
type Vitals struct {
	Version   string    `json:"version"`
	Port      string    `json:"port"`
	PID       int       `json:"pid"`
	StartedAt time.Time `json:"started_at"`
}

// Storage is the data-location panel: where sherlog keeps its files and how much it
// is using (add-health-page). DiskUsageBytes is at most diskCacheTTL stale.
type Storage struct {
	DataDir        string `json:"data_dir"`
	DiskUsageBytes int64  `json:"disk_usage_bytes"`
	OpenSessions   int    `json:"open_sessions"`
	ClosedSessions int    `json:"closed_sessions"`
	TotalEvents    int    `json:"total_events"`
	FieldNotes     int    `json:"field_notes"`
}

// Activity is the recency/throughput panel (add-health-page): what sherlog is doing
// right now. It mirrors the store's activity snapshot plus the daemon-owned SSE gauge.
type Activity struct {
	LastEvent    *time.Time        `json:"last_event,omitempty"` // nil until the first event
	HourlyEvents int               `json:"hourly_events"`        // trailing hour, since daemon start
	Subscribers  int               `json:"subscribers"`          // live Case Board SSE streams
	OpenRun      *store.OpenRunRef `json:"open_run,omitempty"`   // nil when no run is open
}

// Stats is the single document GET /api/stats returns and the Health view renders
// directly (add-health-page D1). It aggregates store activity, daemon gauges, config
// with sources, storage, the stale-probe count, and the self-checks. /health is a
// separate, frozen contract and is untouched by this shape.
type Stats struct {
	Vitals      Vitals               `json:"vitals"`
	Config      config.Effective     `json:"config"` // values + per-key sources (config spec)
	Storage     Storage              `json:"storage"`
	Activity    Activity             `json:"activity"`
	StaleProbes int                  `json:"stale_probes"`
	SelfChecks  map[string]SelfCheck `json:"self_checks"`
}

// handleStats serves the health aggregation (add-health-page D1). GET-only — it is a
// browser-facing read endpoint like the other Case Board routes (case-board-ui D2),
// so it carries no CORS. It always returns 200 even when a self-check fails: a failed
// check is data the view renders, not an endpoint error (log-ingest spec: self-check
// failure is reported, not hidden).
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.buildStats(time.Now()))
}

// buildStats assembles the stats document at the given clock. It is split from the
// handler so tests can build the document directly without an HTTP round-trip, and
// so the "now" used for the hourly window and uptime is injectable.
func (s *Server) buildStats(now time.Time) Stats {
	act := s.store.Stats(now)

	root := s.store.Root()
	storage := Storage{
		DataDir:        root,
		DiskUsageBytes: s.disk.usage(root),
		OpenSessions:   act.OpenSessions,
		ClosedSessions: act.ClosedSessions,
		TotalEvents:    act.TotalEvents,
		FieldNotes:     s.fieldNotesCount(),
	}

	return Stats{
		Vitals: Vitals{
			Version:   s.version,
			Port:      s.cfg.Port,
			PID:       os.Getpid(),
			StartedAt: s.started,
		},
		Config:  s.cfg,
		Storage: storage,
		Activity: Activity{
			LastEvent:    act.LastEvent,
			HourlyEvents: act.HourlyEvents,
			Subscribers:  int(s.subscribers.Load()),
			OpenRun:      act.OpenRun,
		},
		StaleProbes: len(s.store.StaleProbes()),
		SelfChecks: map[string]SelfCheck{
			"storage_writable": s.checkStorageWritable(root),
			"loopback_only":    s.checkLoopbackOnly(),
		},
	}
}

// fieldNotesCount returns the field-notes total, degrading to 0 when the notes store
// is unavailable or unreadable (notes are best-effort telemetry, never a gate — they
// must not break the health view).
func (s *Server) fieldNotesCount() int {
	if s.notes == nil {
		return 0
	}
	n, err := s.notes.Count()
	if err != nil {
		return 0
	}
	return n
}

// checkStorageWritable probes whether the data directory accepts a write by creating
// and deleting a temp file in it (add-health-page D3). This is the failure that
// silently strands every investigation, so the view surfaces it explicitly.
func (s *Server) checkStorageWritable(root string) SelfCheck {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return SelfCheck{OK: false, Detail: "data directory is not writable: " + err.Error()}
	}
	f, err := os.CreateTemp(root, ".sherlog-healthcheck-*")
	if err != nil {
		return SelfCheck{OK: false, Detail: "data directory is not writable: " + err.Error()}
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return SelfCheck{OK: true, Detail: "data directory " + root + " is writable"}
}

// checkLoopbackOnly verifies the listener is bound to the loopback interface
// (add-health-page D3): a non-loopback bind would expose investigation state and
// ingest to the local network, breaking the trust boundary (D4).
func (s *Server) checkLoopbackOnly() SelfCheck {
	if s.bindHost == "127.0.0.1" || s.bindHost == "::1" {
		return SelfCheck{OK: true, Detail: "listening on loopback (" + s.bindHost + ") only"}
	}
	return SelfCheck{OK: false, Detail: "listener is bound to " + s.bindHost + ", not loopback"}
}

// usage returns the data directory's total size in bytes, recomputing it at most once
// per diskCacheTTL (add-health-page D2). A walk error yields the last good value (or
// zero before the first successful walk) rather than failing the endpoint — disk size
// is informational. Only the session directories and top-level files are summed, so a
// huge ~/.sherlog is the cost driver the cache exists to bound.
func (c *diskUsageCache) usage(root string) int64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	if !c.computed.IsZero() && time.Since(c.computed) < diskCacheTTL {
		return c.bytes
	}

	var total int64
	// WalkDir tolerates a missing root (fresh install): the walk func swallows per-
	// entry errors so one unreadable file cannot zero the whole figure.
	_ = filepath.WalkDir(root, func(_ string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, ierr := d.Info(); ierr == nil {
			total += info.Size()
		}
		return nil
	})

	c.bytes = total
	c.computed = time.Now()
	return total
}
