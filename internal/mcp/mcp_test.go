package mcp

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/notes"
	"github.com/neomodular/sherlog/internal/store"
)

// startTestDaemon starts a real daemon HTTP server on a free loopback port over a
// temp-dir store and returns its base URL plus the bound port. The server is shut
// down on test cleanup. This is the same surface the MCP client talks to in
// production (D2), so the test exercises the full client → daemon → store path.
func startTestDaemon(t *testing.T) (base, port string) {
	t.Helper()
	st, err := store.New(store.WithRoot(t.TempDir()))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, err = net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split addr: %v", err)
	}

	srv := &http.Server{Handler: daemon.NewServer(st, "test", config.Default())}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return "http://" + ln.Addr().String(), port
}

// connectMCP wires an in-process MCP server (full tool surface) to a client over
// the SDK's in-memory transport, pointing the daemon client at base. It returns
// the connected client session.
func connectMCP(t *testing.T, ctx context.Context, base, port string) *mcpsdk.ClientSession {
	t.Helper()
	c := &daemonClient{
		base:      base,
		port:      port,
		http:      &http.Client{Timeout: 10 * time.Second},
		awaitHTTP: &http.Client{},
		// Matches startTestDaemon's version so ensureDaemon treats the test daemon
		// as current rather than stale (daemon-self-heal-on-upgrade).
		version: "test",
	}

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "sherlog", Version: "test"}, nil)
	registerTools(server, c)

	st, ct := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

// callTool invokes a tool and decodes its structured output into out. It fails
// the test on a protocol error or a tool error (IsError), surfacing the message.
func callTool(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, name string, args, out any) {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if res.IsError {
		msg := ""
		if len(res.Content) > 0 {
			if tc, ok := res.Content[0].(*mcpsdk.TextContent); ok {
				msg = tc.Text
			}
		}
		t.Fatalf("%s: tool error: %s", name, msg)
	}
	if out == nil {
		return
	}
	// StructuredContent is the typed Out value (ToolHandlerFor populates it);
	// round-trip through JSON to decode into the test's expected shape.
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("%s: marshal structured content: %v", name, err)
	}
	if err := json.Unmarshal(raw, out); err != nil {
		t.Fatalf("%s: decode structured content: %v\nraw: %s", name, err, raw)
	}
}

// firePoke POSTs a probe hit directly to the daemon's ingest endpoint, simulating
// a probe line in the user's code (D3: text/plain simple request, no JSON type).
func firePoke(t *testing.T, base, sessionID, probeID string) {
	t.Helper()
	resp, err := http.Post(base+"/log/"+sessionID+"/"+probeID, "", nil) //nolint:noctx
	if err != nil {
		t.Fatalf("probe POST: %v", err)
	}
	_ = resp.Body.Close()
}

// TestEndToEnd drives the full MCP surface against a live daemon: handshake →
// debug_start → hypotheses + probe registry → simulated probe POSTs → await_run
// → close_run → query_logs → debug_end, asserting the evidence trail at each step
// (task 4.6).
func TestEndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)

	// Handshake produced the tool list.
	tools, err := sess.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 12 {
		names := make([]string, len(tools.Tools))
		for i, tl := range tools.Tools {
			names[i] = tl.Name
		}
		t.Fatalf("tool count = %d, want 12: %v", len(tools.Tools), names)
	}

	// debug_start: session + probe contract.
	var start debugStartOut
	callTool(t, ctx, sess, "debug_start", map[string]any{"bug_description": "login hangs"}, &start)
	if start.SessionID == "" {
		t.Fatal("debug_start: empty session ID")
	}
	// Backward compatible: a caller that omits title still gets a non-empty echoed
	// title — the daemon derives it from the description (mcp-server: Legacy caller).
	if start.Title == "" {
		t.Error("debug_start without a title must echo a derived fallback title")
	}
	if start.ProbeContract.OneLiners["js"] == "" || start.ProbeContract.OneLiners["curl"] == "" {
		t.Fatalf("debug_start: missing probe one-liners: %+v", start.ProbeContract.OneLiners)
	}
	sid := start.SessionID

	// set_hypotheses: three suspects.
	var board setHypothesesOut
	callTool(t, ctx, sess, "set_hypotheses", map[string]any{
		"session_id": sid,
		"hypotheses": []string{"token refresh deadlocks", "DB pool exhausted", "retry loop never exits"},
	}, &board)
	if len(board.Board) != 3 || board.Board[0].ID != "h1" {
		t.Fatalf("set_hypotheses: got %+v", board.Board)
	}

	// register_probe for the suspect we will gather evidence on.
	var probe store.Probe
	callTool(t, ctx, sess, "register_probe", map[string]any{
		"session_id": sid, "id": "p1", "file": "auth.go", "line": 42, "hypothesis_id": "h1",
	}, &probe)
	if probe.ID != "p1" || probe.Removed {
		t.Fatalf("register_probe: got %+v", probe)
	}

	// await_run in the background while probes fire, mirroring the real loop where
	// the user reproduces the bug during the wait (D8).
	type awaitOutcome struct {
		res awaitRunResult
		err error
	}
	done := make(chan awaitOutcome, 1)
	go func() {
		r, e := newAwaitCaller(ctx, sess)("await_run", map[string]any{"session_id": sid, "timeout_s": 10})
		done <- awaitOutcome{r, e}
	}()

	// Give await a moment to open the run, then fire probe hits.
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		firePoke(t, base, sid, "p1")
	}

	out := <-done
	if out.err != nil {
		t.Fatalf("await_run: %v", out.err)
	}
	if out.res.Reason != "quiet" {
		t.Fatalf("await_run reason = %q, want quiet (probes fired)", out.res.Reason)
	}
	// The summary must list p1 with the three hits.
	var p1 *store.ProbeSummary
	for i := range out.res.Summary {
		if out.res.Summary[i].Probe == "p1" {
			p1 = &out.res.Summary[i]
		}
	}
	if p1 == nil || p1.Total != 3 {
		t.Fatalf("await_run summary for p1 = %+v (want total 3)", p1)
	}
	runID := out.res.Run.ID

	// close_run with a verdict.
	var run store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": sid, "verdict": "reproduced"}, &run)
	if run.Verdict != store.VerdictReproduced || run.ClosedAt == nil {
		t.Fatalf("close_run: got %+v", run)
	}

	// query_logs: counts for p1 in the run.
	var q queryLogsOut
	callTool(t, ctx, sess, "query_logs", map[string]any{"session_id": sid, "probe": "p1", "run": runID}, &q)
	if len(q.Results) != 1 || q.Results[0].Total != 3 {
		t.Fatalf("query_logs: got %+v", q.Results)
	}

	// debug_end: p1 was never marked removed, so it must appear in the checklist
	// with file+line and a greppable fragment (D10).
	var end debugEndOut
	callTool(t, ctx, sess, "debug_end", map[string]any{"session_id": sid}, &end)
	if end.CleanupComplete {
		t.Fatal("debug_end: cleanup should be incomplete with an unremoved probe")
	}
	if len(end.UnremovedProbes) != 1 || end.UnremovedProbes[0].File != "auth.go" {
		t.Fatalf("debug_end unremoved = %+v", end.UnremovedProbes)
	}
	wantFrag := base + "/log/" + sid + "/"
	if end.GreppableFragment != wantFrag {
		t.Fatalf("debug_end fragment = %q, want %q", end.GreppableFragment, wantFrag)
	}
}

// TestReportObservationRoundtrip drives the field-notes channel end to end: the
// report_observation tool → daemon /api/notes → field-notes.jsonl, asserting the
// note lands with its session and category and that the tool acknowledges filing
// (field-notes D2/D3, task 2.3).
func TestReportObservationRoundtrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	root := t.TempDir()
	st, err := store.New(store.WithRoot(root))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	srv := &http.Server{Handler: daemon.NewServer(st, "test", config.Default())}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		sctx, c := context.WithTimeout(context.Background(), time.Second)
		defer c()
		_ = srv.Shutdown(sctx)
	})
	base := "http://" + ln.Addr().String()

	sess := connectMCP(t, ctx, base, port)

	var out reportObservationOut
	callTool(t, ctx, sess, "report_observation", map[string]any{
		"note":       "await returned zero events though the user confirmed reproduction; suspect pre-run attribution",
		"category":   "tool-bug",
		"session_id": "a3f9",
	}, &out)
	if !out.Filed {
		t.Fatal("report_observation: filed = false, want true")
	}

	ns, err := notes.New(notes.WithRoot(root))
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}
	list, err := ns.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("note count = %d, want 1", len(list))
	}
	if list[0].Session != "a3f9" || list[0].Category != notes.CategoryToolBug || list[0].Version != "test" {
		t.Errorf("note = %+v", list[0])
	}
}

// TestReportObservationFireAndForget covers design D3: filing never blocks. With
// no daemon reachable, the tool still returns a minimal acknowledgment (filed:
// false) rather than a tool error, so an investigation is never interrupted.
func TestReportObservationFireAndForget(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Point the client at a closed loopback port: ensureDaemon will try (and fail)
	// to reach/spawn a daemon, and the tool must swallow that into filed:false.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	_ = ln.Close() // free the port so nothing answers
	base := "http://127.0.0.1:" + port

	c := &daemonClient{
		base:      base,
		port:      port,
		http:      &http.Client{Timeout: time.Second},
		awaitHTTP: &http.Client{},
		version:   "test",
		// Stub the spawn: the real one would detach a copy of the *test binary* as
		// a fake daemon (os.Executable under `go test`), leaking processes. A no-op
		// spawn keeps the intended shape — the follow-up health wait still fails
		// fast against the dead port, and the tool must swallow that.
		spawn: func() error { return nil },
	}
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "sherlog", Version: "test"}, nil)
	registerTools(server, c)
	stt, ct := mcpsdk.NewInMemoryTransports()
	if _, err := server.Connect(ctx, stt, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-client", Version: "test"}, nil)
	sess, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })

	// The auto-spawn path runs (stubbed above) and the follow-up health wait
	// fails fast against the dead port. Either way the tool must NOT surface an
	// error.
	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{
		Name: "report_observation",
		Arguments: map[string]any{
			"note":     "daemon unreachable, must not block",
			"category": "anomaly",
		},
	})
	if err != nil {
		t.Fatalf("report_observation: protocol error: %v", err)
	}
	if res.IsError {
		t.Fatal("report_observation surfaced a tool error; filing must be fire-and-forget (D3)")
	}
	var out reportObservationOut
	raw, _ := json.Marshal(res.StructuredContent)
	_ = json.Unmarshal(raw, &out)
	if out.Filed {
		t.Error("filed = true with no daemon reachable; want false")
	}
}

// TestCaseLifecycleRecallAndDiff is the task 3.3 E2E: a simulated case is solved
// with a recorded resolution, a fresh debug_start with a similar description
// recalls it through the related-cases section, and diff_runs across that new
// case's reproduce and fixed-check runs flags the probe that stopped firing.
func TestCaseLifecycleRecallAndDiff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)
	call := newAwaitCaller(ctx, sess)

	// --- Case 1: solve it and record a resolution (recall material). ---
	var first debugStartOut
	callTool(t, ctx, sess, "debug_start", map[string]any{
		"title":           "Cart total off by a cent on discounts",
		"bug_description": "checkout total off by a cent on discounted carts",
	}, &first)
	if first.Title != "Cart total off by a cent on discounts" {
		t.Errorf("titled start should echo the title, got %q", first.Title)
	}
	if len(first.RelatedCases) != 0 {
		t.Fatalf("first case should have no recall matches in an empty archive: %+v", first.RelatedCases)
	}
	var board1 setHypothesesOut
	callTool(t, ctx, sess, "set_hypotheses", map[string]any{
		"session_id": first.SessionID,
		"hypotheses": []string{"float rounding in discount calc", "stale price cache", "tax applied twice"},
	}, &board1)
	callTool(t, ctx, sess, "update_hypothesis", map[string]any{
		"session_id": first.SessionID, "id": "h1", "status": "confirmed", "note": "probe showed .005 truncation",
	}, nil)

	// Close it solved: root cause + fix summary + confirmed hypothesis feed recall.
	var end1 debugEndOut
	callTool(t, ctx, sess, "debug_end", map[string]any{
		"session_id":              first.SessionID,
		"root_cause":              "float rounding in discount calc",
		"fix_summary":             "switched discount math to integer cents",
		"confirmed_hypothesis_id": "h1",
	}, &end1)
	if !end1.CleanupComplete {
		t.Fatalf("case 1 had no probes; cleanup should be complete: %+v", end1)
	}

	// --- Case 2: a similar symptom must recall case 1. ---
	var second debugStartOut
	callTool(t, ctx, sess, "debug_start",
		map[string]any{"bug_description": "discount totals wrong by a cent on some carts"}, &second)
	if len(second.RelatedCases) == 0 {
		t.Fatal("recall returned no related cases for a similar discount bug")
	}
	got := second.RelatedCases[0]
	if got.SessionID != first.SessionID {
		t.Errorf("recall top match = %q, want case 1 %q", got.SessionID, first.SessionID)
	}
	// The recalled case is identified by its title so the skill can cite it by name
	// (case-recall: matches identified by title).
	if got.Title != "Cart total off by a cent on discounts" {
		t.Errorf("recall match should carry case 1's title, got %q", got.Title)
	}
	if got.RootCause != "float rounding in discount calc" || got.FixSummary != "switched discount math to integer cents" {
		t.Errorf("recall match missing resolution fields: %+v", got)
	}

	// Probe p1 discriminates the suspect in case 2.
	var probe store.Probe
	callTool(t, ctx, sess, "register_probe", map[string]any{
		"session_id": second.SessionID, "id": "p1", "file": "discount.go", "line": 88, "hypothesis_id": "h1",
	}, &probe)

	// Run 1: reproduce — the buggy path fires p1.
	done1 := make(chan struct{})
	go func() {
		_, _ = call("await_run", map[string]any{"session_id": second.SessionID, "timeout_s": 10})
		close(done1)
	}()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 4; i++ {
		firePoke(t, base, second.SessionID, "p1")
	}
	<-done1
	var reproRun store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": second.SessionID, "verdict": "reproduced"}, &reproRun)

	// Run 2: fixed-check — the fix removed the bad branch, so p1 never fires.
	done2 := make(chan struct{})
	go func() {
		_, _ = call("await_run", map[string]any{"session_id": second.SessionID, "timeout_s": 1})
		close(done2)
	}()
	<-done2
	var fixedRun store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": second.SessionID, "verdict": "fixed-check"}, &fixedRun)

	// --- diff_runs across the reproduce and fixed-check runs. ---
	var diff store.RunDiff
	callTool(t, ctx, sess, "diff_runs", map[string]any{
		"session_id": second.SessionID, "run_a": reproRun.ID, "run_b": fixedRun.ID,
	}, &diff)
	if len(diff.Probes) == 0 {
		t.Fatal("diff_runs returned no probe comparisons")
	}
	// p1 fired in the reproduce run but not the fixed-check run: divergent, pinned
	// first, with the reproduce side carrying its four hits.
	top := diff.Probes[0]
	if top.Probe != "p1" || !top.Divergent {
		t.Fatalf("expected p1 flagged divergent first, got %+v", top)
	}
	if top.A.Run != reproRun.ID || top.A.Total != 4 || !top.A.Fired {
		t.Errorf("reproduce side wrong: %+v", top.A)
	}
	if top.B.Run != fixedRun.ID || top.B.Fired {
		t.Errorf("fixed-check side should show p1 not firing: %+v", top.B)
	}
}

// newAwaitCaller returns a helper that calls a tool and decodes an awaitRunResult,
// usable from a goroutine (it returns the result and error instead of failing the
// test directly, since t.Fatalf from a non-test goroutine is unsafe).
func newAwaitCaller(ctx context.Context, sess *mcpsdk.ClientSession) func(string, any) (awaitRunResult, error) {
	return func(name string, args any) (awaitRunResult, error) {
		res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			return awaitRunResult{}, err
		}
		var out awaitRunResult
		raw, err := json.Marshal(res.StructuredContent)
		if err != nil {
			return awaitRunResult{}, err
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			return awaitRunResult{}, err
		}
		return out, nil
	}
}
