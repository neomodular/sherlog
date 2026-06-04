package daemon

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/neomodular/sherlog/internal/store"
)

// handleEvents is the Case Board's live stream: it bridges the store's in-process
// pub/sub to a Server-Sent Events response for one session (case-board-ui spec:
// Live evidence tail; design D3). The session is selected by the required
// ?session=<id> query param; only that session's events are forwarded so a
// per-case page sees just its own stream.
//
// GET-only: the UI is strictly read-only (design D2). The event types match the
// store's EventKind (log/board/run/probe) and ride the SSE event field so the
// browser can route each with addEventListener.
//
// Backpressure is the store's responsibility, not this handler's: Subscribe hands
// back a buffered channel, and a publish that finds that buffer full drops the
// subscriber and closes the channel (design D3 non-blocking drop). So a stalled
// browser never delays ingest or other subscribers — it is simply dropped, and
// the range below terminates when its channel closes. EventSource then reconnects
// natively on the browser side.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	session := r.URL.Query().Get("session")
	if session == "" {
		writeError(w, http.StatusBadRequest, errors.New("events stream requires a session id"))
		return
	}

	// SSE needs a streaming response: without an http.Flusher the events would buffer
	// until the handler returns, defeating the live tail. If the ResponseWriter
	// cannot flush (it always can for the real server; a recorder cannot), report it
	// rather than silently buffering.
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, errors.New("streaming unsupported"))
		return
	}

	// Subscribe BEFORE writing headers so no event published between header write and
	// subscription can be missed. The defer guarantees the channel is released when
	// the client disconnects or the stream ends (design D3: callers MUST Unsubscribe).
	sub := s.store.Subscribe()
	defer sub.Unsubscribe()

	// Track this live subscriber for the health view's activity panel (add-health-page
	// D2). Increment after a successful Subscribe and decrement on return so the gauge
	// reflects exactly the streams currently connected, no matter how the handler exits.
	s.subscribers.Add(1)
	defer s.subscribers.Add(-1)

	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	// This is a browser-facing read endpoint; the internal /api/ surface carries no
	// CORS, so the stream deliberately advertises none either (design D2).
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ctx := r.Context()
	for {
		select {
		case <-ctx.Done():
			// Client navigated away or the server is shutting down: stop streaming and
			// let the deferred Unsubscribe release the channel.
			return
		case ev, open := <-sub.C:
			if !open {
				// The store dropped this subscriber (slow consumer) and closed the
				// channel — end the response so EventSource reconnects.
				return
			}
			if ev.Session != session {
				continue // not this case's stream
			}
			if err := writeSSE(w, ev); err != nil {
				// A write error means the connection is gone; stop.
				return
			}
			flusher.Flush()
		}
	}
}

// writeSSE serializes one store event as an SSE message: the EventKind becomes the
// SSE event name (so the browser routes by type) and the JSON-encoded event is the
// data line. A single-line data field is sufficient because the JSON is compact
// (no embedded newlines from json.Marshal).
func writeSSE(w http.ResponseWriter, ev store.Event) error {
	payload, err := json.Marshal(ev)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Kind, payload)
	return err
}
