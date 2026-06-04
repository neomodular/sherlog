package store

import "time"

// hourlyBuckets is the size of the per-minute ingest ring (add-health-page D2):
// 60 one-minute buckets cover a trailing hour. The ring is memory-only and zeroed
// on restart, so its hourly count is "since daemon start" — acceptable for a local
// tool's at-a-glance health view, not a historical metric (design Non-Goals).
const hourlyBuckets = 60

// ingestCounters are the activity facts the store already guards under s.mu, lifted
// out so Ingest mutates them in one place (DRY) and Stats reads them atomically with
// the session map (add-health-page D2: counters live in the store). The hourly ring
// is a circular buffer of per-minute counts indexed by minute-since-epoch modulo
// hourlyBuckets; each slot records the epoch-minute it last counted so a stale slot
// (older than an hour) reads as zero rather than double-counting a wrapped minute.
type ingestCounters struct {
	totalEvents int
	lastEvent   time.Time // zero until the first event; UTC

	buckets   [hourlyBuckets]int   // events ingested in each tracked minute
	bucketMin [hourlyBuckets]int64 // epoch-minute owning each bucket slot
}

// recordIngest bumps the activity counters for one ingested event at ts. Callers
// hold s.mu (it runs inside Ingest's critical section, before the off-lock append),
// so the counters stay consistent with the in-memory event record.
func (c *ingestCounters) recordIngest(ts time.Time) {
	c.totalEvents++
	c.lastEvent = ts

	minute := ts.Unix() / 60
	slot := int(minute % hourlyBuckets)
	if c.bucketMin[slot] != minute {
		// This slot belongs to an older (wrapped-around) minute: reset it before
		// counting so a minute an hour ago can never inflate the current window.
		c.bucketMin[slot] = minute
		c.buckets[slot] = 0
	}
	c.buckets[slot]++
}

// hourlyTotal sums the buckets whose owning minute falls within the trailing hour
// of now, discarding any slot left over from a minute more than 60 buckets ago.
// Callers hold s.mu.
func (c *ingestCounters) hourlyTotal(now time.Time) int {
	cutoff := now.Unix()/60 - hourlyBuckets
	total := 0
	for i := 0; i < hourlyBuckets; i++ {
		if c.bucketMin[i] > cutoff {
			total += c.buckets[i]
		}
	}
	return total
}

// OpenRunRef identifies the single currently-open run for the health view's
// activity panel (add-health-page): the session it belongs to and the run ID. Both
// empty means no run is open anywhere.
type OpenRunRef struct {
	Session string `json:"session"`
	Run     string `json:"run"`
}

// Activity is the store's contribution to /api/stats (add-health-page D2): the
// process-independent facts about ingest and session state. The daemon merges this
// with its own gauges (subscribers, vitals, self-checks) into the stats document.
type Activity struct {
	TotalEvents    int         `json:"total_events"`
	LastEvent      *time.Time  `json:"last_event,omitempty"` // nil until the first event
	HourlyEvents   int         `json:"hourly_events"`        // trailing hour, since daemon start
	OpenSessions   int         `json:"open_sessions"`
	ClosedSessions int         `json:"closed_sessions"`
	OpenRun        *OpenRunRef `json:"open_run,omitempty"` // nil when no run is open
}

// Stats returns the store's activity snapshot for the health view, reading the
// ingest counters and session map under one lock so the numbers are mutually
// consistent (add-health-page D2). The hourly count is relative to now. When
// several sessions hold an open run the most recently created session's run wins,
// matching ResumeLatest's "the session you are working in" intent.
func (s *Store) Stats(now time.Time) Activity {
	s.mu.Lock()
	defer s.mu.Unlock()

	act := Activity{
		TotalEvents:  s.counters.totalEvents,
		HourlyEvents: s.counters.hourlyTotal(now),
	}
	if !s.counters.lastEvent.IsZero() {
		t := s.counters.lastEvent
		act.LastEvent = &t
	}

	var openRunSession *Session
	for _, entry := range s.sessions {
		sess := entry.session
		if sess.ClosedAt != nil {
			act.ClosedSessions++
			continue
		}
		act.OpenSessions++
		// Track the open run on the most recently created open session so the panel
		// names the run the user is actively in when several sessions overlap.
		if hasOpenRun(sess) && (openRunSession == nil || sess.CreatedAt.After(openRunSession.CreatedAt)) {
			openRunSession = sess
		}
	}
	if openRunSession != nil {
		act.OpenRun = &OpenRunRef{Session: openRunSession.ID, Run: latestOpenRunID(openRunSession)}
	}
	return act
}

// hasOpenRun reports whether a session has any run still awaiting a verdict.
func hasOpenRun(sess *Session) bool {
	for i := range sess.Runs {
		if sess.Runs[i].ClosedAt == nil {
			return true
		}
	}
	return false
}

// latestOpenRunID returns the ID of the session's most recently opened run with no
// verdict, mirroring LatestOpenRun's selection without re-acquiring the lock.
func latestOpenRunID(sess *Session) string {
	for i := len(sess.Runs) - 1; i >= 0; i-- {
		if sess.Runs[i].ClosedAt == nil {
			return sess.Runs[i].ID
		}
	}
	return ""
}
