package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/neomodular/sherlog/internal/config"
	"github.com/neomodular/sherlog/internal/daemon"
	"github.com/neomodular/sherlog/internal/notes"
	"github.com/neomodular/sherlog/internal/store"
)

// daemonClient talks to the resident daemon's internal /api/ surface over
// localhost HTTP (D2). It owns daemon health-checking and detached auto-spawn so
// the MCP tools never have to think about whether the daemon is up.
type daemonClient struct {
	base string       // e.g. http://127.0.0.1:2218
	port string       // resolved port (SHERLOG_PORT or default), for probe URLs
	http *http.Client // short-timeout client for control calls
	// awaitHTTP has no overall timeout: await_run long-polls for up to its
	// requested duration, so a fixed client timeout would cut the wait short.
	awaitHTTP *http.Client
}

// healthInfo is the daemon's /health payload; the version field is how the MCP
// process confirms the listener is sherlog and not a foreign process on the port
// (D2: foreign-port error path).
type healthInfo struct {
	Version string `json:"version"`
	Uptime  string `json:"uptime"`
}

// newDaemonClient resolves the daemon address through the same config resolution
// the daemon uses (env > file > default), so the MCP process and the daemon never
// disagree on the port even when it is set via config.json rather than
// SHERLOG_PORT (design D2/D4). A config load failure falls back to the brand port
// rather than blocking the MCP server from starting. It does not contact the
// daemon.
func newDaemonClient() *daemonClient {
	port := daemon.DefaultPort
	if root, err := config.DefaultRoot(); err == nil {
		if cfg, err := config.Load(root); err == nil {
			port = cfg.Port
		}
	}
	return &daemonClient{
		base:      "http://" + net.JoinHostPort("127.0.0.1", port),
		port:      port,
		http:      &http.Client{Timeout: 10 * time.Second},
		awaitHTTP: &http.Client{}, // no timeout: bounded by the request context
	}
}

// probeURLTemplate is the URL skeleton probes POST to for a session (D3, D4). The
// <probe> placeholder is filled per probe by the skill; the port flows from the
// daemon so a SHERLOG_PORT override propagates into every probe line (D4).
func (c *daemonClient) probeURLTemplate(sessionID string) string {
	return c.base + "/log/" + sessionID + "/<probe>"
}

// ensureDaemon guarantees a sherlog daemon is answering on the port, spawning one
// detached if needed (D2). It distinguishes three states:
//   - sherlog already listening      → nil
//   - nothing listening              → spawn `sherlog daemon`, wait for /health
//   - foreign process on the port    → error explaining SHERLOG_PORT (D4)
func (c *daemonClient) ensureDaemon(ctx context.Context) error {
	switch info, err := c.health(ctx); {
	case err == nil && info.Version != "":
		return nil // sherlog is up
	case err == nil:
		// Port answered /health but did not identify as sherlog. Treat any other
		// listener as foreign — see portOccupiedByForeign for the non-/health case.
		return c.foreignPortError()
	}

	// Nothing answered /health. Before spawning, check whether *something* owns
	// the port: a foreign server that simply lacks a /health route would 404
	// rather than refuse the connection. Spawning into an occupied port would
	// just fail to bind, so surface the conflict directly.
	if c.portOccupiedByForeign(ctx) {
		return c.foreignPortError()
	}

	if err := c.spawnDaemon(); err != nil {
		return fmt.Errorf("auto-spawn daemon: %w", err)
	}
	return c.waitForHealth(ctx)
}

// foreignPortError is the actionable message for a non-sherlog listener (D4).
func (c *daemonClient) foreignPortError() error {
	return fmt.Errorf(
		"port %s is held by a process that is not the sherlog daemon — stop it or set SHERLOG_PORT to a free port (probe URLs follow the daemon's port automatically)",
		c.port)
}

// portOccupiedByForeign reports whether the port refuses a fresh TCP dial. A
// successful dial means *something* is listening that is not sherlog (we already
// failed /health), i.e. a foreign process.
func (c *daemonClient) portOccupiedByForeign(ctx context.Context) bool {
	d := net.Dialer{Timeout: 500 * time.Millisecond}
	conn, err := d.DialContext(ctx, "tcp", net.JoinHostPort("127.0.0.1", c.port))
	if err != nil {
		return false // connection refused: nothing is listening, safe to spawn
	}
	_ = conn.Close()
	return true
}

// spawnDaemon launches `sherlog daemon` as a detached background process so it
// outlives this MCP process (D2). Detachment is platform-specific (setsid on
// Unix, DETACHED_PROCESS on Windows) and lives in spawn_*.go behind detachAttrs.
func (c *daemonClient) spawnDaemon() error {
	exe, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate sherlog executable: %w", err)
	}
	cmd := exec.Command(exe, "daemon")
	// The child inherits SHERLOG_PORT from this process's environment so it binds
	// the same port the client is targeting.
	cmd.Env = os.Environ()
	cmd.Stdin = nil
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.SysProcAttr = detachAttrs()
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start daemon process: %w", err)
	}
	// Release the child: we never wait on it, so reap it from our process table
	// to avoid a zombie on Unix. The daemon keeps running independently.
	_ = cmd.Process.Release()
	return nil
}

// waitForHealth polls /health until the freshly spawned daemon answers as sherlog
// or the budget elapses. The daemon binds and serves in well under a second, so a
// short overall budget with tight polling keeps first-use latency low.
func (c *daemonClient) waitForHealth(ctx context.Context) error {
	deadline := time.Now().Add(5 * time.Second)
	for {
		if info, err := c.health(ctx); err == nil && info.Version != "" {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("daemon spawned but did not become healthy on port %s within timeout", c.port)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// health GETs /health. A transport error means nothing is listening; a non-200
// or unparseable body means something is listening that is not the sherlog
// daemon (info.Version stays empty), which the caller treats as foreign.
func (c *daemonClient) health(ctx context.Context) (healthInfo, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+"/health", nil)
	if err != nil {
		return healthInfo{}, err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return healthInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return healthInfo{}, nil // listening but not /health-OK: foreign
	}
	var info healthInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return healthInfo{}, nil // listening but not sherlog's JSON: foreign
	}
	return info, nil
}

// --- internal /api/ calls mapping onto daemon endpoints (server.go routes) ---

// createSessionResult mirrors the daemon's create-session response. Preferences
// ride this response (design D4) so the plugin never reads the config file.
type createSessionResult struct {
	Session         *store.Session      `json:"session"`
	ExistingSameCWD *store.Session      `json:"existing_same_cwd"`
	RelatedCases    []store.RecallMatch `json:"related_cases"` // possibly-related closed cases (case-recall)
	Preferences     preferences         `json:"preferences"`
}

// preferences is the skill-presentation block delivered through debug_start
// (design D4): verbosity and color, resolved by the daemon from effective config.
type preferences struct {
	Verbosity string `json:"verbosity"`
	Color     string `json:"color"`
}

func (c *daemonClient) createSession(ctx context.Context, title, description, cwd string) (createSessionResult, error) {
	var out createSessionResult
	err := c.call(ctx, http.MethodPost, "/api/sessions", map[string]any{
		"title": title, "description": description, "cwd": cwd,
	}, &out)
	return out, err
}

// sessionState is a session detail payload plus its computed repro rate, mirroring
// the daemon's resume / session-detail envelope (harden-detective-gates D-I). The
// embedded store.Session promotes its fields — including the pinned commit (D-H) — to
// the top level, so it decodes the daemon's promoted shape and doubles as the
// debug_resume tool output. Callers that need only the session ID read it through the
// embedded field (sess.ID); repro_rate is additive.
type sessionState struct {
	store.Session
	ReproRate store.ReproRate `json:"repro_rate"`
}

func (c *daemonClient) resumeLatest(ctx context.Context) (*sessionState, error) {
	var out sessionState
	if err := c.call(ctx, http.MethodGet, "/api/sessions", nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *daemonClient) getSession(ctx context.Context, id string) (*sessionState, error) {
	var out sessionState
	if err := c.call(ctx, http.MethodGet, "/api/sessions/"+id, nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// closeSessionResult mirrors the daemon's close-session response (unremoved probes).
type closeSessionResult struct {
	UnremovedProbes []store.Probe `json:"unremoved_probes"`
}

// closeSession closes a session, optionally recording its resolution (D4). A nil
// resolution sends no body, closing the case unsolved; the daemon treats an
// all-empty resolution as unsolved too, so existing callers stay unaffected. The
// prevention references (regression_test_ref, guardrail) ride the body when present
// (harden-detective-gates D-J); the daemon records them, never executes them.
func (c *daemonClient) closeSession(ctx context.Context, id string, res *store.Resolution) (closeSessionResult, error) {
	var body any
	if res != nil {
		m := map[string]any{
			"root_cause":              res.RootCause,
			"fix_summary":             res.FixSummary,
			"confirmed_hypothesis_id": res.ConfirmedHypothesisID,
			"regression_test_ref":     res.RegressionTestRef,
		}
		if res.Guardrail != nil {
			m["guardrail"] = map[string]any{"type": res.Guardrail.Type, "ref": res.Guardrail.Ref}
		}
		body = m
	}
	var out closeSessionResult
	err := c.call(ctx, http.MethodDelete, "/api/sessions/"+id, body, &out)
	return out, err
}

// diffRunsResult is a run diff plus each compared run's recorded fix prediction,
// mirroring the daemon's diff envelope (harden-detective-gates D-D). The embedded
// store.RunDiff promotes its fields, so a client decoding a bare RunDiff is
// unaffected; the predictions let the divergence be judged against the recorded claim
// rather than conversation memory. It doubles as the diff_runs tool output.
type diffRunsResult struct {
	store.RunDiff
	PredictionA string `json:"prediction_a,omitempty"`
	PredictionB string `json:"prediction_b,omitempty"`
}

// diffRuns fetches the per-probe comparison of two runs of a session for the
// diff_runs tool (log-query spec: Run diff). The daemon validates the run pair,
// returns divergent probes first, and attaches each run's recorded prediction (D-D).
func (c *daemonClient) diffRuns(ctx context.Context, id, runA, runB string) (diffRunsResult, error) {
	q := url.Values{}
	q.Set("a", runA)
	q.Set("b", runB)
	var out diffRunsResult
	err := c.call(ctx, http.MethodGet, "/api/sessions/"+id+"/diff?"+q.Encode(), nil, &out)
	return out, err
}

func (c *daemonClient) setHypotheses(ctx context.Context, id string, statements []string) ([]store.Hypothesis, error) {
	var out []store.Hypothesis
	err := c.call(ctx, http.MethodPut, "/api/sessions/"+id+"/hypotheses",
		map[string]any{"statements": statements}, &out)
	return out, err
}

// updateHypothesis routes a status change to the daemon, carrying the evidence
// citation (probe_id, run_id) that a kill or confirm requires (harden-detective-gates
// D-B). The daemon cross-checks the citation against its own registry; a refine
// (status active) sends empty citation fields the daemon exempts.
func (c *daemonClient) updateHypothesis(ctx context.Context, id, hid, status, note, probeID, runID string) (store.Hypothesis, error) {
	var out store.Hypothesis
	err := c.call(ctx, http.MethodPatch, "/api/sessions/"+id+"/hypotheses/"+hid,
		map[string]any{"status": status, "note": note, "probe_id": probeID, "run_id": runID}, &out)
	return out, err
}

func (c *daemonClient) registerProbe(ctx context.Context, id string, p store.Probe) (store.Probe, error) {
	var out store.Probe
	err := c.call(ctx, http.MethodPost, "/api/sessions/"+id+"/probes", p, &out)
	return out, err
}

func (c *daemonClient) removeProbe(ctx context.Context, id, pid string) error {
	return c.call(ctx, http.MethodDelete, "/api/sessions/"+id+"/probes/"+pid, nil, nil)
}

// awaitRunResult mirrors the daemon's await response (await.go awaitResult),
// including the session's computed repro rate with raw counts (harden-detective-gates
// D-I) so the result surfaces a computed — not asserted — determinism signal.
type awaitRunResult struct {
	Run       store.Run            `json:"run"`
	Summary   []store.ProbeSummary `json:"summary"`
	Reason    string               `json:"reason"`
	TotalSeen int                  `json:"total_seen"`
	ReproRate store.ReproRate      `json:"repro_rate"`
}

// awaitRun long-polls /await using the no-timeout client; the daemon honors the
// requested timeout and the surrounding context bounds the wait (D8). The optional
// fix prediction is forwarded so the daemon stamps it on the run at call receipt —
// before any summary is returned — as the prerequisite for a fixed-check close (D-D).
func (c *daemonClient) awaitRun(ctx context.Context, id string, timeoutS int, prediction string) (awaitRunResult, error) {
	var out awaitRunResult
	err := c.callWith(ctx, c.awaitHTTP, http.MethodPost, "/api/sessions/"+id+"/await",
		map[string]any{"timeout_s": timeoutS, "prediction": prediction}, &out)
	return out, err
}

func (c *daemonClient) closeRun(ctx context.Context, id, verdict string) (store.Run, error) {
	var out store.Run
	err := c.call(ctx, http.MethodPost, "/api/sessions/"+id+"/runs/close",
		map[string]any{"verdict": verdict}, &out)
	return out, err
}

func (c *daemonClient) queryLogs(ctx context.Context, id string, f store.QueryFilter) ([]store.QueryResult, error) {
	q := url.Values{}
	if f.Run != "" {
		q.Set("run", f.Run)
	}
	if f.Probe != "" {
		q.Set("probe", f.Probe)
	}
	if f.Limit > 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	path := "/api/sessions/" + id + "/query"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out []store.QueryResult
	err := c.call(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// blastRadiusResult mirrors the daemon's blast-radius envelope (add-blast-radius,
// mcp-server spec): the recorded radius (pattern, note, searched_at, truncated, hits)
// plus its derived unreviewed count. The embedded store.BlastRadius promotes its
// fields — pattern/hits/truncated decode directly on the wire — so a client decoding a
// bare radius is unaffected; unreviewed_count is additive and doubles as the
// map_blast_radius / annotate_blast_radius tool output.
type blastRadiusResult struct {
	store.BlastRadius
	UnreviewedCount int `json:"unreviewed_count"`
}

// blastAnnotationBody is one per-hit verdict on the annotate wire (add-blast-radius
// D-D). The JSON field names match the daemon's strict decoder exactly — unknown
// fields are 400s — so file/line/verdict/note are named verbatim.
type blastAnnotationBody struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Verdict string `json:"verdict"`
	Note    string `json:"note,omitempty"`
}

// mapBlastRadius passes the agent-authored pattern to the daemon for execution and
// decodes the recorded radius (add-blast-radius D-A). The daemon compiles the pattern,
// walks the session cwd, and enforces the false-coverage gate; a compile error, an
// empty pattern, or a gate rejection comes back as a non-2xx whose message callWith
// surfaces verbatim for the tool to relay.
func (c *daemonClient) mapBlastRadius(ctx context.Context, id, pattern, note string) (blastRadiusResult, error) {
	var out blastRadiusResult
	err := c.call(ctx, http.MethodPost, "/api/sessions/"+id+"/blast-radius",
		map[string]any{"pattern": pattern, "note": note}, &out)
	return out, err
}

// annotateBlastRadius merges the agent's per-hit verdicts into the recorded radius via
// the daemon (add-blast-radius D-D). The daemon set-checks each {file, line} against
// the recorded hits and re-validates the verdict enum; a rejection (unknown site,
// invalid verdict, no radius) surfaces verbatim.
func (c *daemonClient) annotateBlastRadius(ctx context.Context, id string, anns []blastAnnotationBody) (blastRadiusResult, error) {
	var out blastRadiusResult
	err := c.call(ctx, http.MethodPost, "/api/sessions/"+id+"/blast-radius/annotations",
		map[string]any{"annotations": anns}, &out)
	return out, err
}

// reportObservation files one field note against the daemon (field-notes D2).
// The daemon stamps the version; the session is the active investigation when one
// is open, else empty.
func (c *daemonClient) reportObservation(ctx context.Context, session, category, note string) (notes.Note, error) {
	var out notes.Note
	err := c.call(ctx, http.MethodPost, "/api/notes", map[string]any{
		"session": session, "category": category, "note": note,
	}, &out)
	return out, err
}

// --- HTTP plumbing ---

// call issues a control request against the daemon using the short-timeout
// client and decodes the JSON response into out (nil to ignore the body).
func (c *daemonClient) call(ctx context.Context, method, path string, body, out any) error {
	return c.callWith(ctx, c.http, method, path, body, out)
}

// callWith is call parameterized by HTTP client so await can use the no-timeout
// one. A non-2xx response is turned into an error carrying the daemon's message.
func (c *daemonClient) callWith(ctx context.Context, hc *http.Client, method, path string, body, out any) error {
	var rdr io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		rdr = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, rdr)
	if err != nil {
		return fmt.Errorf("build request %s %s: %w", method, path, err)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("daemon request %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("daemon %s %s: %s", method, path, daemonError(resp))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode daemon response %s %s: %w", method, path, err)
	}
	return nil
}

// daemonError extracts the daemon's {"error":...} message from a non-2xx
// response, falling back to the HTTP status line.
func daemonError(resp *http.Response) string {
	var e struct {
		Error string `json:"error"`
	}
	if json.NewDecoder(resp.Body).Decode(&e) == nil && e.Error != "" {
		return e.Error
	}
	return resp.Status
}
