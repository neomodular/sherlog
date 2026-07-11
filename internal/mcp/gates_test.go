package mcp

import (
	"context"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/store"
)

// The gates suite covers the MCP tool-schema half of harden-detective-gates
// (Group 3): the new params flow to the daemon, the daemon's gate rejections surface
// verbatim as tool errors, and the added result fields (pinned commit, repro rate,
// fix predictions, resolution references) ride the tool payloads. It exercises the
// full client → daemon → store path against a live daemon over a temp-dir store, the
// same surface production uses (D2).

// startTestDaemonWithStore serves a caller-provided store on a free loopback port and
// returns its base URL and bound port, shutting the server down on cleanup. Tests that
// need to inject store behavior (e.g. a deterministic commit resolver) build the store
// themselves; startTestDaemon covers the default case.
func startTestDaemonWithStore(t *testing.T, st *store.Store) (base, port string) {
	t.Helper()
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

// callToolExpectErr invokes a tool expecting a tool error (IsError) and returns its
// message. It fails the test on a protocol error or an unexpected success, so a gate
// that should have rejected the call cannot pass silently.
func callToolExpectErr(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, name string, args any) string {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", name, err)
	}
	if !res.IsError {
		t.Fatalf("%s: expected a tool error, got success", name)
	}
	if len(res.Content) == 0 {
		t.Fatalf("%s: tool error carried no content", name)
	}
	tc, ok := res.Content[0].(*mcpsdk.TextContent)
	if !ok {
		t.Fatalf("%s: tool error content is not text: %T", name, res.Content[0])
	}
	return tc.Text
}

// startCase opens a session through debug_start and returns its ID.
func startCase(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, desc string) string {
	t.Helper()
	var out debugStartOut
	callTool(t, ctx, sess, "debug_start", map[string]any{"bug_description": desc}, &out)
	if out.SessionID == "" {
		t.Fatal("debug_start returned an empty session ID")
	}
	return out.SessionID
}

// setThreeSuspects installs a minimal valid board (the store floor is three, D-E).
func setThreeSuspects(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, sid string) {
	t.Helper()
	var board setHypothesesOut
	callTool(t, ctx, sess, "set_hypotheses", map[string]any{
		"session_id": sid,
		"hypotheses": []string{"first suspect", "second suspect", "third suspect"},
	}, &board)
}

// registerPredictedProbe registers a probe carrying a differing prediction pair at a
// real absolute path (so the location gate passes, D-G) — the shape the confirm gate
// requires of a confirming probe (D-A/D-C).
func registerPredictedProbe(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, sid, pid, hid string) {
	t.Helper()
	callTool(t, ctx, sess, "register_probe", map[string]any{
		"session_id": sid, "id": pid, "file": tempProbeFile(t, 100), "line": 10, "hypothesis_id": hid,
		"expected_if_true": "signal present", "expected_if_false": "signal absent",
	}, nil)
}

// seedClosedRun opens a run through await_run (no probe activity, so it returns at the
// short timeout) and closes it with the given verdict, returning the closed run. The
// await runs in a goroutine because it blocks until timeout; the run is open once it
// returns, so the follow-up close targets it.
func seedClosedRun(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, sid, verdict string) store.Run {
	t.Helper()
	call := newAwaitCaller(ctx, sess)
	done := make(chan struct{})
	go func() {
		_, _ = call("await_run", map[string]any{"session_id": sid, "timeout_s": 1})
		close(done)
	}()
	<-done
	var run store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": sid, "verdict": verdict}, &run)
	return run
}

// TestRegisterProbePredictionsAndLocation covers the register_probe requirement
// (mcp-server spec): the location check rejects a nonexistent file and an
// out-of-range line naming the resolved path, and a valid predicted probe registers
// with its predictions echoed back.
func TestRegisterProbePredictionsAndLocation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)
	sid := startCase(t, ctx, sess, "probe location gate")
	setThreeSuspects(t, ctx, sess, sid)

	t.Run("nonexistent file rejected", func(t *testing.T) {
		msg := callToolExpectErr(t, ctx, sess, "register_probe", map[string]any{
			"session_id": sid, "id": "p1", "file": "src/ghost.js", "line": 1, "hypothesis_id": "h1",
		})
		// The error names the resolved path (D-G) and says the file was not found.
		if !strings.Contains(msg, "ghost.js") || !strings.Contains(msg, "not found") {
			t.Errorf("nonexistent-file error should name the resolved path and say not found, got: %s", msg)
		}
	})

	t.Run("line beyond end of file rejected", func(t *testing.T) {
		f := tempProbeFile(t, 120)
		msg := callToolExpectErr(t, ctx, sess, "register_probe", map[string]any{
			"session_id": sid, "id": "p2", "file": f, "line": 900, "hypothesis_id": "h1",
		})
		if !strings.Contains(msg, "120 lines") {
			t.Errorf("out-of-range error should state the file has 120 lines, got: %s", msg)
		}
	})

	t.Run("valid predicted probe registers", func(t *testing.T) {
		f := tempProbeFile(t, 50)
		var probe store.Probe
		callTool(t, ctx, sess, "register_probe", map[string]any{
			"session_id": sid, "id": "p3", "file": f, "line": 10, "hypothesis_id": "h1",
			"expected_if_true": "token populated", "expected_if_false": "token nil",
		}, &probe)
		if probe.ID != "p3" {
			t.Fatalf("register_probe: got %+v", probe)
		}
		// The response echoes the predictions (mcp-server spec).
		if probe.ExpectedIfTrue != "token populated" || probe.ExpectedIfFalse != "token nil" {
			t.Errorf("predictions not echoed: %+v", probe)
		}
	})
}

// TestUpdateHypothesisCitations covers the update_hypothesis requirement (mcp-server
// spec): a kill/confirm with no citation is rejected client-side before reaching the
// daemon, and a daemon-side confirm rejection (no reproduced run) surfaces its repair
// instruction verbatim.
func TestUpdateHypothesisCitations(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)

	t.Run("missing citation rejected client-side", func(t *testing.T) {
		// A bogus session id proves the check runs before the daemon: a client-side
		// rejection talks about the missing citation, not an unknown session.
		msg := callToolExpectErr(t, ctx, sess, "update_hypothesis", map[string]any{
			"session_id": "no-such-session", "id": "h1", "status": "killed", "note": "hunch",
		})
		if !strings.Contains(msg, "probe_id") || !strings.Contains(msg, "run_id") {
			t.Errorf("client-side rejection should name probe_id and run_id, got: %s", msg)
		}
		if strings.Contains(msg, "session") {
			t.Errorf("rejection reached the daemon (mentions session) instead of failing client-side: %s", msg)
		}
	})

	t.Run("store rejection surfaces verbatim", func(t *testing.T) {
		sid := startCase(t, ctx, sess, "confirm without a reproduced run")
		setThreeSuspects(t, ctx, sess, sid)
		registerPredictedProbe(t, ctx, sess, sid, "p1", "h1")
		// One closed run, but not-reproduced: the citation is well-formed (probe + closed
		// run) yet the confirm gate has no reproduced run to stand on (D-C).
		run := seedClosedRun(t, ctx, sess, sid, "not-reproduced")
		msg := callToolExpectErr(t, ctx, sess, "update_hypothesis", map[string]any{
			"session_id": sid, "id": "h1", "status": "confirmed", "note": "sure",
			"probe_id": "p1", "run_id": run.ID,
		})
		// The daemon's D-C repair instruction is surfaced unaltered.
		if !strings.Contains(msg, "get at least one run closed reproduced first") {
			t.Errorf("tool error should carry the daemon's repair instruction verbatim, got: %s", msg)
		}
	})
}

// TestAwaitRunPredictionAndReproRate covers the await_run requirement (mcp-server
// spec): a fix prediction supplied through the tool is recorded on the run before the
// call's summary returns, and the result reports the session's repro rate with counts.
func TestAwaitRunPredictionAndReproRate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)
	call := newAwaitCaller(ctx, sess)

	t.Run("prediction recorded through the tool", func(t *testing.T) {
		sid := startCase(t, ctx, sess, "record a fix prediction")
		const prediction = "p1 token now populated; p5 fires zero times"
		res, err := call("await_run", map[string]any{
			"session_id": sid, "timeout_s": 1, "prediction": prediction,
		})
		if err != nil {
			t.Fatalf("await_run: %v", err)
		}
		// The returned run already carries the prediction — it was stamped at call
		// receipt, before this summary was returned (D-D).
		if res.Run.Prediction != prediction {
			t.Errorf("run prediction = %q, want %q", res.Run.Prediction, prediction)
		}
	})

	t.Run("repro rate reported", func(t *testing.T) {
		sid := startCase(t, ctx, sess, "repro rate over closed runs")
		seedClosedRun(t, ctx, sess, sid, "reproduced")
		seedClosedRun(t, ctx, sess, sid, "reproduced")
		seedClosedRun(t, ctx, sess, sid, "not-reproduced")
		// A further await opens a new (open) run; the rate is computed over the closed
		// runs only, so it reports 2/3.
		res, err := call("await_run", map[string]any{"session_id": sid, "timeout_s": 1})
		if err != nil {
			t.Fatalf("await_run: %v", err)
		}
		if res.ReproRate.Reproduced != 2 || res.ReproRate.NotReproduced != 1 {
			t.Fatalf("repro counts = %+v, want 2 reproduced / 1 not-reproduced", res.ReproRate)
		}
		if got := res.ReproRate.Rate; got < 0.66 || got > 0.67 {
			t.Errorf("repro rate = %v, want ~0.667 (2/3)", got)
		}
	})
}

// TestDebugStartCommit covers the debug_start requirement (mcp-server spec): the
// start payload carries the pinned commit when the cwd is a git work tree and omits
// it otherwise. A deterministic commit resolver is injected so the test never depends
// on the environment being a repository.
func TestDebugStartCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const sha = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4e5f6a1b2"

	t.Run("commit pinned when resolvable", func(t *testing.T) {
		st, err := store.New(store.WithRoot(t.TempDir()), store.WithCommitResolver(func(string) string { return sha }))
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		base, port := startTestDaemonWithStore(t, st)
		sess := connectMCP(t, ctx, base, port)

		var out debugStartOut
		callTool(t, ctx, sess, "debug_start", map[string]any{"bug_description": "from a work tree"}, &out)
		if out.Commit != sha {
			t.Errorf("start commit = %q, want the pinned SHA %q", out.Commit, sha)
		}
	})

	t.Run("commit omitted outside a work tree", func(t *testing.T) {
		st, err := store.New(store.WithRoot(t.TempDir()), store.WithCommitResolver(func(string) string { return "" }))
		if err != nil {
			t.Fatalf("store.New: %v", err)
		}
		base, port := startTestDaemonWithStore(t, st)
		sess := connectMCP(t, ctx, base, port)

		var out debugStartOut
		callTool(t, ctx, sess, "debug_start", map[string]any{"bug_description": "no repo here"}, &out)
		if out.Commit != "" {
			t.Errorf("start commit = %q, want empty outside a work tree", out.Commit)
		}
	})
}

// TestDebugResumeCarriesReproRateAndCommit covers the debug_resume half of the
// debug_start requirement (mcp-server spec): resume returns the pinned commit and the
// computed repro rate alongside the session state.
func TestDebugResumeCarriesReproRateAndCommit(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const sha = "0f1e2d3c4b5a60718293a4b5c6d7e8f90a1b2c3d"
	st, err := store.New(store.WithRoot(t.TempDir()), store.WithCommitResolver(func(string) string { return sha }))
	if err != nil {
		t.Fatalf("store.New: %v", err)
	}
	base, port := startTestDaemonWithStore(t, st)
	sess := connectMCP(t, ctx, base, port)

	sid := startCase(t, ctx, sess, "resume carries repro rate and commit")
	seedClosedRun(t, ctx, sess, sid, "reproduced")
	seedClosedRun(t, ctx, sess, sid, "not-reproduced")

	var resumed sessionState
	callTool(t, ctx, sess, "debug_resume", map[string]any{"session_id": sid}, &resumed)
	if resumed.ID != sid {
		t.Fatalf("resumed the wrong session: %s", resumed.ID)
	}
	if resumed.Commit != sha {
		t.Errorf("resume commit = %q, want %q", resumed.Commit, sha)
	}
	if resumed.ReproRate.Reproduced != 1 || resumed.ReproRate.NotReproduced != 1 {
		t.Errorf("resume repro counts = %+v, want 1/1", resumed.ReproRate)
	}
}

// TestDebugEndReferencesAndRejection covers the debug_end requirement (mcp-server
// spec): a solved close carries the prevention references through to storage, and a
// solved close naming an unconfirmed hypothesis is rejected verbatim while the session
// stays open with its board intact.
func TestDebugEndReferencesAndRejection(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)

	t.Run("solved close with references", func(t *testing.T) {
		sid := startCase(t, ctx, sess, "solved close records references")
		setThreeSuspects(t, ctx, sess, sid)
		registerPredictedProbe(t, ctx, sess, sid, "p1", "h1")
		run := seedClosedRun(t, ctx, sess, sid, "reproduced")
		callTool(t, ctx, sess, "update_hypothesis", map[string]any{
			"session_id": sid, "id": "h1", "status": "confirmed", "note": "confirmed",
			"probe_id": "p1", "run_id": run.ID,
		}, nil)
		callTool(t, ctx, sess, "remove_probe", map[string]any{"session_id": sid, "id": "p1"}, nil)

		var end debugEndOut
		callTool(t, ctx, sess, "debug_end", map[string]any{
			"session_id":              sid,
			"root_cause":              "the confirmed cause",
			"fix_summary":             "the applied fix",
			"confirmed_hypothesis_id": "h1",
			"regression_test_ref":     "TestRefreshRace",
			"guardrail":               map[string]any{"type": "test", "ref": "ci/regression-suite"},
		}, &end)

		// The references persisted: read the now-closed session back by ID.
		var closed sessionState
		callTool(t, ctx, sess, "debug_resume", map[string]any{"session_id": sid}, &closed)
		if closed.Resolution == nil {
			t.Fatal("closed session carries no resolution")
		}
		if closed.Resolution.RegressionTestRef != "TestRefreshRace" {
			t.Errorf("regression_test_ref = %q, want TestRefreshRace", closed.Resolution.RegressionTestRef)
		}
		if closed.Resolution.Guardrail == nil || closed.Resolution.Guardrail.Type != "test" ||
			closed.Resolution.Guardrail.Ref != "ci/regression-suite" {
			t.Errorf("guardrail not persisted: %+v", closed.Resolution.Guardrail)
		}
	})

	t.Run("rejected solved close leaves the session open", func(t *testing.T) {
		sid := startCase(t, ctx, sess, "solved close on an unconfirmed board")
		setThreeSuspects(t, ctx, sess, sid)
		// h1 is still active — a solved close naming it must be rejected (D-F).
		msg := callToolExpectErr(t, ctx, sess, "debug_end", map[string]any{
			"session_id":              sid,
			"root_cause":              "premature",
			"fix_summary":             "premature",
			"confirmed_hypothesis_id": "h1",
		})
		if !strings.Contains(msg, "is not confirmed on the board") {
			t.Errorf("rejection should carry the daemon's repair instruction, got: %s", msg)
		}
		// The session is still open with its board intact.
		var open sessionState
		callTool(t, ctx, sess, "debug_resume", map[string]any{}, &open)
		if open.ID != sid {
			t.Fatalf("latest open session = %s, want the un-closed %s", open.ID, sid)
		}
		if open.ClosedAt != nil {
			t.Error("session was closed despite the rejected solved close")
		}
		if len(open.Hypotheses) != 3 {
			t.Errorf("board should be intact with 3 suspects, got %d", len(open.Hypotheses))
		}
	})
}

// TestDiffRunsPrediction covers the diff_runs requirement (mcp-server spec): when a
// compared run carries a recorded prediction, the diff result includes it so the
// divergence is judged against the recorded claim.
func TestDiffRunsPrediction(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)
	call := newAwaitCaller(ctx, sess)

	sid := startCase(t, ctx, sess, "diff surfaces the fixed-check prediction")
	setThreeSuspects(t, ctx, sess, sid)
	callTool(t, ctx, sess, "register_probe", map[string]any{
		"session_id": sid, "id": "p1", "file": tempProbeFile(t, 60), "line": 5, "hypothesis_id": "h1",
	}, nil)

	// Reproduce run: p1 fires.
	done := make(chan struct{})
	go func() {
		_, _ = call("await_run", map[string]any{"session_id": sid, "timeout_s": 10})
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	for i := 0; i < 3; i++ {
		firePoke(t, base, sid, "p1")
	}
	<-done
	var reproRun store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": sid, "verdict": "reproduced"}, &reproRun)

	// Fixed-check run: opened with a fix prediction (the prerequisite for the verdict).
	const prediction = "p1 fires zero times now"
	done2 := make(chan struct{})
	go func() {
		_, _ = call("await_run", map[string]any{"session_id": sid, "timeout_s": 1, "prediction": prediction})
		close(done2)
	}()
	<-done2
	var fixedRun store.Run
	callTool(t, ctx, sess, "close_run", map[string]any{"session_id": sid, "verdict": "fixed-check"}, &fixedRun)

	var diff diffRunsResult
	callTool(t, ctx, sess, "diff_runs", map[string]any{
		"session_id": sid, "run_a": reproRun.ID, "run_b": fixedRun.ID,
	}, &diff)
	// run_b is the fixed-check run, so its prediction rides prediction_b.
	if diff.PredictionB != prediction {
		t.Errorf("diff prediction_b = %q, want %q", diff.PredictionB, prediction)
	}
	// The reproduce run carried no prediction, so prediction_a stays empty.
	if diff.PredictionA != "" {
		t.Errorf("diff prediction_a = %q, want empty", diff.PredictionA)
	}
}
