package daemon

import (
	"log"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// retentionInterval is how often the daemon re-prunes closed sessions past the
// retention window (configuration spec: "at startup and every 24 hours").
const retentionInterval = 24 * time.Hour

// startRetention prunes closed sessions older than retentionDays once immediately,
// then on a daily ticker (configuration spec: Retention pruning). A retentionDays
// of 0 (the default) disables pruning entirely — sessions are kept forever — so no
// goroutine is started. Pruning logs the count and IDs it deleted to the daemon
// log; open sessions are never pruned (enforced in the store).
func startRetention(st *store.Store, retentionDays int) {
	if retentionDays <= 0 {
		return // 0 = keep forever; nothing to schedule
	}

	window := time.Duration(retentionDays) * 24 * time.Hour
	prune(st, window) // run once at startup

	ticker := time.NewTicker(retentionInterval)
	go func() {
		for range ticker.C {
			prune(st, window)
		}
	}()
}

// prune deletes closed sessions whose close time is older than window and logs the
// result. A store error is logged, never fatal — retention failure must not bring
// down the daemon or block ingest.
func prune(st *store.Store, window time.Duration) {
	cutoff := time.Now().Add(-window)
	pruned, err := st.PruneClosedBefore(cutoff)
	if err != nil {
		log.Printf("sherlog: retention prune error: %v", err)
	}
	if len(pruned) > 0 {
		log.Printf("sherlog: retention pruned %d closed session(s): %v", len(pruned), pruned)
	}
}
