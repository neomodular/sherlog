package store

import "time"

// DefaultFloodN is the default first/last-N retained per probe per run (D8).
const DefaultFloodN = 20

// floodBuffer bounds in-memory log storage for one (run, probe) pair: it keeps
// the first N and last N events plus an exact total counter; the middle is
// dropped (D8). This caps memory under a probe firing in a hot loop while still
// disclosing the true volume and showing both ends of the timeline.
//
// adopted tracks how much of total was attributed by pre-run adoption rather than
// direct ingest (fix-prerun design D4); it is a disclosed minimum when truncation
// straddled the adoption boundary (D3).
type floodBuffer struct {
	n     int
	first []LogEvent
	last  []LogEvent // ring of the most recent up-to-n events
	total int
	// adopted is the count of total attributed by pre-run adoption (D4).
	adopted int
	// forceTruncated marks a buffer whose totals are disclosed minima because a
	// flood truncation straddled the adoption boundary (D3); truncated() then
	// reports true even when the post-split counters would not.
	forceTruncated bool
}

func newFloodBuffer(n int) *floodBuffer {
	if n < 1 {
		n = DefaultFloodN
	}
	return &floodBuffer{n: n}
}

// add records an event, retaining it only if it falls in the first-N or last-N
// window. total always reflects the true count.
func (b *floodBuffer) add(ev LogEvent) {
	b.total++
	if len(b.first) < b.n {
		b.first = append(b.first, ev)
	}
	if len(b.last) < b.n {
		b.last = append(b.last, ev)
		return
	}
	// last is full: slide the window forward, dropping the oldest.
	copy(b.last, b.last[1:])
	b.last[len(b.last)-1] = ev
}

// truncated reports whether any middle events were dropped, i.e. the total
// exceeds what the first/last windows can jointly retain. Computed from counters
// rather than events() so callers reading both never traverse the buffer twice.
func (b *floodBuffer) truncated() bool {
	return b.forceTruncated || b.total > b.retained()
}

// retained reports how many events events() would return, without building them.
// It mirrors events()'s de-duplication of the first/last overlap: below the gap
// threshold every event is kept; once a middle gap opens, first-N plus last-N.
func (b *floodBuffer) retained() int {
	if b.total <= 2*b.n {
		return b.total
	}
	return len(b.first) + len(b.last)
}

// events returns the retained events in chronological order, de-duplicating the
// overlap between the first and last windows when total ≤ 2N (no middle gap).
func (b *floodBuffer) events() []LogEvent {
	if b.total <= b.n {
		// Everything fits in first; last is a prefix-equal subset.
		return append([]LogEvent(nil), b.first...)
	}
	if b.total <= 2*b.n {
		// first and last overlap; last begins at index (total-n), so the tail of
		// last after the first window is exactly events n..total-1.
		overlap := len(b.first) + len(b.last) - b.total
		out := append([]LogEvent(nil), b.first...)
		out = append(out, b.last[overlap:]...)
		return out
	}
	// Genuine gap in the middle: first N then last N.
	out := append([]LogEvent(nil), b.first...)
	out = append(out, b.last...)
	return out
}

// splitAdopt partitions an orphan buffer at the adoption window: retained events
// with after(ev.TS) true are removed from this buffer and returned as a fresh
// buffer whose events are marked with run (events keep their re-keyed Run so
// replay and re-key agree). The remaining (pre-window) events stay in this buffer.
//
// Totals: when this buffer never truncated, every event is retained, so both
// sides split exactly. When the split genuinely straddles the window — pre- and
// post-boundary events both present (len(keep)>0 AND len(move)>0) AND the buffer
// truncated — the dropped middle events could belong to either side, so both
// totals become disclosed minima (count of retained events) carrying the
// truncation flag (design D3). When every event is post-boundary (len(keep)==0)
// the truncation did NOT straddle the boundary: the whole buffer moves and its
// exact counter b.total transfers intact, even if truncated (the normal
// total>retained disclosure still surfaces the dropped middle). Returns nil when
// no retained event falls in the window.
func (b *floodBuffer) splitAdopt(run string, after func(time.Time) bool) *floodBuffer {
	all := b.events()
	var keep, move []LogEvent
	for _, ev := range all {
		if after(ev.TS) {
			ev.Run = run
			move = append(move, ev)
		} else {
			keep = append(keep, ev)
		}
	}
	if len(move) == 0 {
		return nil
	}

	wasTruncated := b.truncated()
	straddled := wasTruncated && len(keep) > 0
	adopted := newFloodBuffer(b.n)
	for _, ev := range move {
		adopted.add(ev)
	}
	// All events moved: the exact counter transfers; nothing was split off, so the
	// adopted total is precise (truncated() still discloses any dropped middle).
	if len(keep) == 0 {
		adopted.total = b.total
	}
	adopted.adopted = adopted.total
	// Re-fill this buffer from the events that remain orphaned. Rebuilding keeps
	// first/last windows and the total consistent with the reduced set.
	b.first, b.last, b.total = nil, nil, 0
	for _, ev := range keep {
		b.add(ev)
	}

	// A truncation that straddled the boundary means dropped middle events could
	// belong to either side: surface both totals as disclosed minima (D3).
	if straddled {
		adopted.total = len(move)
		adopted.adopted = len(move)
		adopted.forceTruncated = true
		b.forceTruncated = true
	}
	return adopted
}
