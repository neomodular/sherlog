package daemon

import (
	"bufio"
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/store"
)

// liveServer starts the real HTTP server on an ephemeral loopback port and returns
// its base URL plus the backing store, so SSE tests exercise actual streaming with
// a flusher (an httptest recorder cannot stream). Cleanup is registered on t.
func liveServer(t *testing.T) (base string, st *store.Store) {
	t.Helper()
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	hs := &http.Server{Handler: NewServer(st, "test", config.Default())}
	go hs.Serve(ln)
	t.Cleanup(func() { hs.Close() })
	return "http://" + ln.Addr().String(), st
}

// openSSE connects to the events stream for a session and returns the response and
// a buffered reader positioned just after the initial flush. The caller closes the
// response body. ctx lets a test cancel the long-lived request.
func openSSE(t *testing.T, ctx context.Context, base, session string) (*http.Response, *bufio.Reader) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/events?session="+session, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("connect SSE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("SSE content-type = %q, want text/event-stream", ct)
	}
	return resp, bufio.NewReader(resp.Body)
}

// readSSEEvent reads one SSE message (lines until a blank line) and returns the
// event name and data payload. Fails the test if no event arrives before deadline.
func readSSEEvent(t *testing.T, r *bufio.Reader, deadline time.Duration) (event, data string) {
	t.Helper()
	type frame struct{ event, data string }
	got := make(chan frame, 1)
	go func() {
		var ev, dt string
		for {
			line, err := r.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				ev = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				dt = strings.TrimPrefix(line, "data: ")
			case line == "": // end of one message
				if ev != "" || dt != "" {
					got <- frame{ev, dt}
					return
				}
			}
		}
	}()
	select {
	case f := <-got:
		return f.event, f.data
	case <-time.After(deadline):
		t.Fatalf("no SSE event within %v", deadline)
		return "", ""
	}
}

// TestSSEDeliveryDuringIngest covers the live evidence tail (case-board-ui spec):
// with the case open in the browser, a probe firing during a run is delivered as a
// typed `log` event over the SSE stream without a reload.
func TestSSEDeliveryDuringIngest(t *testing.T) {
	base, st := liveServer(t)
	sess, _, err := st.CreateSession("bug", "/tmp/app")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, r := openSSE(t, ctx, base, sess.ID)
	defer resp.Body.Close()

	// Fire from a goroutine so the read below is already waiting on the stream. A
	// transport failure surfaces as the read timing out, so the goroutine ignores
	// errors (it must not touch t off the test goroutine).
	go asyncPost(base+"/log/"+sess.ID+"/p1", `{"x":1}`)

	event, data := readSSEEvent(t, r, 2*time.Second)
	if event != string(store.EventLog) {
		t.Errorf("event = %q, want %q", event, store.EventLog)
	}
	if !strings.Contains(data, `"kind":"log"`) || !strings.Contains(data, `"probe":"p1"`) {
		t.Errorf("event data missing log/probe fields: %s", data)
	}
	if !strings.Contains(data, `"session":"`+sess.ID+`"`) {
		t.Errorf("event data not scoped to session: %s", data)
	}
}

// TestSSEOnlyOwnSession covers the per-session filter (design D3): a stream for one
// session never receives another session's events.
func TestSSEOnlyOwnSession(t *testing.T) {
	base, st := liveServer(t)
	a, _, _ := st.CreateSession("bug a", "/tmp/a")
	b, _, _ := st.CreateSession("bug b", "/tmp/b")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, r := openSSE(t, ctx, base, a.ID)
	defer resp.Body.Close()

	// Fire into B (other session) first, then A. The stream must skip B and deliver
	// A's event, proving the filter drops foreign-session traffic.
	httpPost(t, base+"/log/"+b.ID+"/p9", `{"other":1}`)
	go asyncPost(base+"/log/"+a.ID+"/p1", `{"mine":1}`)

	_, data := readSSEEvent(t, r, 2*time.Second)
	if strings.Contains(data, `"p9"`) || !strings.Contains(data, `"p1"`) {
		t.Errorf("stream leaked another session's event or missed its own: %s", data)
	}
}

// TestSSEStalledSubscriberDropped covers the non-blocking drop guarantee
// (case-board-ui spec: a slow browser does not block the daemon). One subscriber
// stops reading entirely; ingest beyond the per-subscriber buffer must still
// complete promptly and a second, actively-reading subscriber must keep receiving
// events. The stalled connection is dropped by the store rather than stalling the
// publisher.
func TestSSEStalledSubscriberDropped(t *testing.T) {
	base, st := liveServer(t)
	sess, _, _ := st.CreateSession("bug", "/tmp/app")

	// Stalled subscriber: connect, then never read its body.
	stallCtx, stallCancel := context.WithCancel(context.Background())
	defer stallCancel()
	stallResp, _ := openSSE(t, stallCtx, base, sess.ID)
	defer stallResp.Body.Close()

	// Active subscriber: drains its stream.
	liveCtx, liveCancel := context.WithCancel(context.Background())
	defer liveCancel()
	liveResp, liveReader := openSSE(t, liveCtx, base, sess.ID)
	defer liveResp.Body.Close()

	// Flood well past the per-subscriber buffer. Ingest must stay fast despite the
	// stalled subscriber — if a full buffer blocked the publisher this would hang.
	const flood = 300
	start := time.Now()
	for i := 0; i < flood; i++ {
		httpPost(t, base+"/log/"+sess.ID+"/p1", `{"i":1}`)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("ingest under a stalled subscriber took %v; publisher appears blocked", elapsed)
	}

	// The active subscriber still receives events: the stall did not poison fan-out.
	event, _ := readSSEEvent(t, liveReader, 2*time.Second)
	if event != string(store.EventLog) {
		t.Errorf("active subscriber event = %q, want %q", event, store.EventLog)
	}
}

// TestEventsRequiresSession covers the missing-session-id guard: the stream needs a
// session to scope to, so an absent param is a 400, not an empty stream.
func TestEventsRequiresSession(t *testing.T) {
	srv, _ := newTestServer(t)
	w := do(srv, http.MethodGet, "/api/events", "")
	if w.Code != http.StatusBadRequest {
		t.Errorf("events without session = %d, want 400", w.Code)
	}
}

// httpPost issues a probe-style POST and fails the test on a transport error or a
// non-2xx status. Used by the live SSE tests to drive ingest over the wire from the
// test goroutine.
func httpPost(t *testing.T, url, body string) {
	t.Helper()
	resp, err := http.Post(url, "", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		t.Fatalf("POST %s status = %d", url, resp.StatusCode)
	}
}

// asyncPost is the goroutine-safe ingest used when a test fires while already
// blocked reading the SSE stream. It cannot touch *testing.T off the test
// goroutine, so a transport failure is ignored — it surfaces as the awaited read
// timing out, which the test goroutine reports.
func asyncPost(url, body string) {
	resp, err := http.Post(url, "", strings.NewReader(body))
	if err == nil {
		resp.Body.Close()
	}
}
