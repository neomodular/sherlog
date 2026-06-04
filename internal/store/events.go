package store

import "sync"

// EventKind labels a store change so an SSE bridge can route it to the right
// browser handler (design D3): one stream, typed events.
type EventKind string

const (
	// EventLog is a newly ingested probe hit (flood-control aware).
	EventLog EventKind = "log"
	// EventBoard is a hypothesis board change (set or status/note update).
	EventBoard EventKind = "board"
	// EventRun is a run lifecycle change (open or close).
	EventRun EventKind = "run"
	// EventProbe is a probe registry change (register or remove).
	EventProbe EventKind = "probe"
)

// Event is one store change broadcast to subscribers. Session scopes it so a
// per-session SSE handler can filter to its own stream (design D3). Payload
// carries the changed record (a LogEvent, Hypothesis, Run, or Probe copy) so the
// bridge can serialize it without a second store read.
type Event struct {
	Kind    EventKind `json:"kind"`
	Session string    `json:"session"`
	Payload any       `json:"payload,omitempty"`
}

// subBufferN is the per-subscriber backlog before a slow consumer is dropped
// (design D3): generous enough to ride out a browser GC pause, small enough that
// a dead subscriber cannot pin unbounded memory.
const subBufferN = 64

// subscription is one live subscriber: a buffered channel plus a once-guarded
// close so an unsubscribe racing a publish-side drop closes the channel exactly
// once (a double close would panic).
type subscription struct {
	ch        chan Event
	closeOnce sync.Once
}

func (sub *subscription) close() { sub.closeOnce.Do(func() { close(sub.ch) }) }

// Subscription is the consumer handle returned by Subscribe: a receive-only event
// channel and the Unsubscribe that releases it. The channel is closed when the
// subscriber is dropped (by Unsubscribe or by a publish that found the buffer
// full), so a range over C terminates cleanly either way.
type Subscription struct {
	C           <-chan Event
	unsubscribe func()
}

// Unsubscribe stops delivery and closes C. It is idempotent and safe to call from
// any goroutine, so an SSE handler can defer it on request-context cancellation.
func (s Subscription) Unsubscribe() { s.unsubscribe() }

// Subscribe registers a new subscriber and returns its handle. Subscribers are
// independent: each gets its own buffered channel, so one stalled browser never
// blocks ingest or other subscribers (design D3). The caller MUST Unsubscribe to
// avoid leaking the channel.
func (s *Store) Subscribe() Subscription {
	sub := &subscription{ch: make(chan Event, subBufferN)}

	s.subMu.Lock()
	if s.subs == nil {
		s.subs = make(map[*subscription]struct{})
	}
	s.subs[sub] = struct{}{}
	s.subMu.Unlock()

	return Subscription{
		C:           sub.ch,
		unsubscribe: func() { s.dropSubscriber(sub) },
	}
}

// dropSubscriber removes a subscriber and closes its channel exactly once. Shared
// by Unsubscribe and the publish-side stall drop so the close/delete is in one
// place (DRY) and cannot double-close.
func (s *Store) dropSubscriber(sub *subscription) {
	s.subMu.Lock()
	if _, ok := s.subs[sub]; ok {
		delete(s.subs, sub)
		sub.close()
	}
	s.subMu.Unlock()
}

// publish fans an event out to every subscriber without blocking: a subscriber
// whose buffer is full is dropped and its channel closed rather than stalling the
// publisher (design D3 non-blocking drop). Callers MUST NOT hold s.mu — publish
// takes subMu, a separate lock, so event delivery never serializes against the
// hot ingest/state path, and a subscriber draining its channel cannot deadlock a
// publisher waiting on s.mu.
func (s *Store) publish(ev Event) {
	s.subMu.Lock()
	var stalled []*subscription
	for sub := range s.subs {
		select {
		case sub.ch <- ev:
		default:
			// Buffer full: the subscriber is too slow. Mark for drop; closing under
			// the same lock keeps the map and channel state consistent.
			stalled = append(stalled, sub)
		}
	}
	for _, sub := range stalled {
		delete(s.subs, sub)
		sub.close()
	}
	s.subMu.Unlock()
}
