package daemon

import (
	"context"
	"sort"
	"time"

	"github.com/neomodular/sherlog/internal/store"
)

// Default await tuning (D8). debounceQuiet is how long log flow must stay quiet
// after first activity before await returns early; pollInterval is how often the
// engine samples event counts to detect activity and quiet.
const (
	defaultDebounceQuiet = 2 * time.Second
	defaultPollInterval  = 100 * time.Millisecond
)

// awaitEngine implements the open-or-attach run + blocking wait of D8. It is
// stateless beyond its tuning knobs and the store it observes: open-or-attach is
// a single atomic store operation (store.OpenOrAttachRun), so concurrent await
// calls on the same session converge on the same run with no locking here.
type awaitEngine struct {
	store    *store.Store
	debounce time.Duration
	poll     time.Duration
}

func newAwaitEngine(s *store.Store) *awaitEngine {
	return &awaitEngine{store: s, debounce: defaultDebounceQuiet, poll: defaultPollInterval}
}

// awaitResult is what an await call resolves to: the run it attached to, the
// per-probe summary for that run, and how it ended.
type awaitResult struct {
	Run       store.Run            `json:"run"`
	Summary   []store.ProbeSummary `json:"summary"`
	Reason    string               `json:"reason"`     // "quiet", "timeout", or "deadline"
	TotalSeen int                  `json:"total_seen"` // events observed in the run during this wait
}

// await opens a run (or re-attaches to the session's already-open run, D8) and
// blocks until log flow quiets after first activity or the timeout elapses.
//
// Quiet detection works by sampling the run's total event count: once the count
// has grown at least once (first activity), the engine returns as soon as the
// count holds steady for the debounce window. With no activity at all it returns
// at timeout reporting zero events so the skill can run a connectivity check.
func (e *awaitEngine) await(ctx context.Context, sessionID string, timeout time.Duration) (awaitResult, error) {
	// Atomically open a run or re-attach to the session's already-open one, so
	// re-invocation while a run is open is idempotent and concurrent awaits
	// converge on the same run (D8).
	run, err := e.store.OpenOrAttachRun(sessionID)
	if err != nil {
		return awaitResult{}, err
	}

	deadline := time.Now().Add(timeout)
	baseline := e.runTotal(sessionID, run.ID) // events already attributed before this wait
	lastCount := baseline
	var lastChange time.Time // zero until first activity is seen

	ticker := time.NewTicker(e.poll)
	defer ticker.Stop()

	reason := "timeout"
	for {
		select {
		case <-ctx.Done():
			reason = "deadline"
			return e.finish(sessionID, run, baseline, reason)
		case now := <-ticker.C:
			count := e.runTotal(sessionID, run.ID)
			if count != lastCount {
				lastCount = count
				lastChange = now
			}
			// Return early only after activity has begun and then gone quiet.
			if !lastChange.IsZero() && now.Sub(lastChange) >= e.debounce {
				reason = "quiet"
				return e.finish(sessionID, run, baseline, reason)
			}
			if now.After(deadline) {
				return e.finish(sessionID, run, baseline, reason)
			}
		}
	}
}

// finish assembles the result from the run's current per-probe summary, then
// zero-fills every registered probe that never fired in the run. The store
// reports only probes that actually fired (fired-only contract); the spec
// requires the summary to list "for each registered probe" — a probe firing
// zero times (p3:0) is the signal the skill uses to kill hypotheses (D7), and
// it must be distinguishable from "no data".
func (e *awaitEngine) finish(sessionID string, run store.Run, baseline int, reason string) (awaitResult, error) {
	summary, err := e.store.RunSummary(sessionID, run.ID)
	if err != nil {
		return awaitResult{}, err
	}
	total := 0
	for _, ps := range summary {
		total += ps.Total
	}

	sess, err := e.store.GetSession(sessionID)
	if err != nil {
		return awaitResult{}, err
	}
	summary = mergeRegisteredProbes(summary, sess.Probes, run.ID)

	return awaitResult{Run: run, Summary: summary, Reason: reason, TotalSeen: total - baseline}, nil
}

// mergeRegisteredProbes appends a zero-count ProbeSummary for every registered
// probe absent from the fired-only summary, then re-sorts by probe ID so the
// result lists every registered probe (e.g. p1:2, p2:14, p3:0).
func mergeRegisteredProbes(summary []store.ProbeSummary, probes []store.Probe, runID string) []store.ProbeSummary {
	fired := make(map[string]bool, len(summary))
	for _, ps := range summary {
		fired[ps.Probe] = true
	}
	for _, p := range probes {
		if !fired[p.ID] {
			summary = append(summary, store.ProbeSummary{Probe: p.ID, Run: runID, Total: 0, Events: nil})
		}
	}
	sort.Slice(summary, func(i, j int) bool { return summary[i].Probe < summary[j].Probe })
	return summary
}

// runTotal reads the run's true event count as a single integer activity signal.
// It uses the store's counter-only RunTotal so polling ~10x/sec never allocates
// per-probe summaries, even under a hot-loop probe (D8). A store error (e.g. the
// session vanished) reads as zero activity, letting the wait fall through to its
// timeout rather than crash the poll loop.
func (e *awaitEngine) runTotal(sessionID, runID string) int {
	total, err := e.store.RunTotal(sessionID, runID)
	if err != nil {
		return 0
	}
	return total
}
