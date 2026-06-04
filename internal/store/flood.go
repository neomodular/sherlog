package store

// DefaultFloodN is the default first/last-N retained per probe per run (D8).
const DefaultFloodN = 20

// floodBuffer bounds in-memory log storage for one (run, probe) pair: it keeps
// the first N and last N events plus an exact total counter; the middle is
// dropped (D8). This caps memory under a probe firing in a hot loop while still
// disclosing the true volume and showing both ends of the timeline.
type floodBuffer struct {
	n     int
	first []LogEvent
	last  []LogEvent // ring of the most recent up-to-n events
	total int
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
// exceeds what the first/last windows can jointly retain.
func (b *floodBuffer) truncated() bool {
	return b.total > len(b.events())
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
