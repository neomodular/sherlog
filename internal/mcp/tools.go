package mcp

import (
	"context"
	"fmt"
	"os"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/neomodular/sherlog/internal/store"
)

// registerTools installs the full investigation tool surface (D9) on the server,
// each handler routing through the daemon client. Every handler first ensures the
// daemon is up (D2) so a tool call on a fresh machine self-heals.
func registerTools(server *mcpsdk.Server, c *daemonClient) {
	add(server, c, "debug_start",
		"Open a new debugging investigation. Returns the session ID, the probe URL "+
			"template, and fire-and-forget probe one-liners for JS/browser, Python, Go, "+
			"Ruby, and curl. Start every debug session here.",
		debugStart)

	add(server, c, "debug_resume",
		"Reconstruct an investigation after context loss: returns the full state "+
			"(bug, hypothesis board, probe registry, runs) for the latest open session, "+
			"or a specific session by ID.",
		debugResume)

	add(server, c, "debug_end",
		"Close the investigation and get the cleanup checklist: every probe not yet "+
			"marked removed (with file and line) plus the greppable URL fragment to "+
			"verify zero leftover probes remain in the code. When the case is solved, "+
			"pass root_cause, fix_summary, and confirmed_hypothesis_id so it becomes "+
			"recall material for future investigations; omit them to close unsolved.",
		debugEnd)

	add(server, c, "diff_runs",
		"Compare two runs of the active investigation probe-by-probe: which probes "+
			"fired in each run, their counts, and first/last sample values, with "+
			"divergent probes (fired in only one run, or ≥10× count difference) listed "+
			"first. Use it to confirm a root cause — e.g. diff a reproduce run against a "+
			"fixed-check run. Returns a compact comparison, never raw logs.",
		diffRuns)

	add(server, c, "set_hypotheses",
		"Replace the hypothesis board with a list of suspect statements (provide at "+
			"least three). Each becomes an active hypothesis with an ID (h1, h2, ...).",
		setHypotheses)

	add(server, c, "update_hypothesis",
		"Update a hypothesis's status (active, killed, confirmed) and attach an "+
			"evidence note explaining why it was killed or confirmed.",
		updateHypothesis)

	add(server, c, "register_probe",
		"Record a placed probe in the registry so cleanup is guaranteed findable: "+
			"its ID, file, line, and the hypothesis it discriminates.",
		registerProbe)

	add(server, c, "remove_probe",
		"Mark a probe removed after its line has been deleted from the code.",
		removeProbe)

	add(server, c, "await_run",
		"Open (or re-attach to) a run and block until probe activity goes quiet or "+
			"the timeout elapses (default 120s). Re-invoke after a timeout to keep "+
			"waiting on the same run for long reproductions. Returns a per-probe "+
			"summary, never raw logs.",
		awaitRun)

	add(server, c, "close_run",
		"Record the user's verdict on the latest open run: reproduced, "+
			"not-reproduced, or fixed-check.",
		closeRun)

	add(server, c, "query_logs",
		"Query collected evidence by probe and/or run with a limit: returns counts "+
			"and selected first/last events per bucket, with truncation disclosed. "+
			"Never dumps raw logs.",
		queryLogs)

	addReportObservation(server, c)
}

// toolFn is an investigation tool handler over the daemon client. Keeping the
// client out of the SDK signature lets every handler stay a plain function the
// generic adapter wires up.
type toolFn[In, Out any] func(ctx context.Context, c *daemonClient, in In) (Out, error)

// add adapts a toolFn into the SDK's ToolHandlerFor, injecting the shared client
// and ensuring the daemon is up before each call (D2). Errors are returned as
// tool errors (ToolHandlerFor packs them into IsError content) so the model sees
// and can react to them rather than the call failing at the protocol level.
func add[In, Out any](server *mcpsdk.Server, c *daemonClient, name, desc string, fn toolFn[In, Out]) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{Name: name, Description: desc},
		func(ctx context.Context, _ *mcpsdk.CallToolRequest, in In) (*mcpsdk.CallToolResult, Out, error) {
			var zero Out
			if err := c.ensureDaemon(ctx); err != nil {
				return nil, zero, err
			}
			out, err := fn(ctx, c, in)
			if err != nil {
				return nil, zero, err
			}
			return nil, out, nil
		})
}

// --- debug_start / debug_resume / debug_end (D9, 4.3) ---

type debugStartIn struct {
	BugDescription string `json:"bug_description" jsonschema:"the bug being investigated"`
}

type debugStartOut struct {
	SessionID     string         `json:"session_id"`
	ProbeContract probeContract  `json:"probe_contract"`
	Preferences   preferences    `json:"preferences"`             // skill presentation (design D4)
	WarnSameCWD   *store.Session `json:"warn_same_cwd,omitempty"` // a concurrent open session here
	// RelatedCases are possibly-related solved cases recall surfaced for this bug
	// description (case-recall spec). They are leads the skill may cite when forming
	// hypotheses — never evidence; probes remain the only evidence.
	RelatedCases []store.RecallMatch `json:"related_cases,omitempty"`
}

func debugStart(ctx context.Context, c *daemonClient, in debugStartIn) (debugStartOut, error) {
	cwd, _ := os.Getwd() // best-effort: same-cwd warning is advisory
	res, err := c.createSession(ctx, in.BugDescription, cwd)
	if err != nil {
		return debugStartOut{}, fmt.Errorf("debug_start: %w", err)
	}
	return debugStartOut{
		SessionID:     res.Session.ID,
		ProbeContract: buildProbeContract(c.probeURLTemplate(res.Session.ID)),
		Preferences:   res.Preferences,
		WarnSameCWD:   res.ExistingSameCWD,
		RelatedCases:  res.RelatedCases,
	}, nil
}

type debugResumeIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"resume this session; omit for the latest open one"`
}

func debugResume(ctx context.Context, c *daemonClient, in debugResumeIn) (*store.Session, error) {
	var (
		sess *store.Session
		err  error
	)
	if in.SessionID != "" {
		sess, err = c.getSession(ctx, in.SessionID)
	} else {
		sess, err = c.resumeLatest(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("debug_resume: %w", err)
	}
	return sess, nil
}

type debugEndIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"the session to close; omit for the latest open one"`
	// Optional resolution recorded at close so the case becomes recall material
	// (mcp-server spec). Omit all three to close unsolved; existing callers that send
	// none keep working unchanged. Supply them only when a root cause was confirmed.
	RootCause             string `json:"root_cause,omitempty" jsonschema:"the confirmed root cause, when solved"`
	FixSummary            string `json:"fix_summary,omitempty" jsonschema:"a concise summary of the fix, when solved"`
	ConfirmedHypothesisID string `json:"confirmed_hypothesis_id,omitempty" jsonschema:"the hypothesis confirmed as culprit, e.g. h2"`
}

type debugEndOut struct {
	UnremovedProbes   []store.Probe `json:"unremoved_probes"`   // each with file+line for cleanup
	GreppableFragment string        `json:"greppable_fragment"` // grep this; require zero matches (D10)
	CleanupComplete   bool          `json:"cleanup_complete"`   // true when no probes remain unremoved
}

func debugEnd(ctx context.Context, c *daemonClient, in debugEndIn) (debugEndOut, error) {
	// D9 spells debug_end() with no argument; resolve the latest open session when
	// session_id is omitted, matching the latest-or-named pattern used by
	// debug_resume and close_run. An explicit ID still closes that specific session.
	sessionID := in.SessionID
	if sessionID == "" {
		sess, err := c.resumeLatest(ctx)
		if err != nil {
			return debugEndOut{}, fmt.Errorf("debug_end: %w", err)
		}
		sessionID = sess.ID
	}
	// Pass the resolution through only when at least one field is set; an all-empty
	// resolution and a nil one both close the case unsolved (D4), so omitting the
	// new fields preserves the prior debug_end behavior exactly.
	var resolution *store.Resolution
	if in.RootCause != "" || in.FixSummary != "" || in.ConfirmedHypothesisID != "" {
		resolution = &store.Resolution{
			RootCause:             in.RootCause,
			FixSummary:            in.FixSummary,
			ConfirmedHypothesisID: in.ConfirmedHypothesisID,
		}
	}
	res, err := c.closeSession(ctx, sessionID, resolution)
	if err != nil {
		return debugEndOut{}, fmt.Errorf("debug_end: %w", err)
	}
	return debugEndOut{
		UnremovedProbes:   res.UnremovedProbes,
		GreppableFragment: greppableFragment(c.probeURLTemplate(sessionID)),
		CleanupComplete:   len(res.UnremovedProbes) == 0,
	}, nil
}

// --- board + probe registry (D9, 4.4) ---

type setHypothesesIn struct {
	SessionID  string   `json:"session_id"`
	Hypotheses []string `json:"hypotheses" jsonschema:"suspect statements; provide at least three"`
}

// setHypothesesOut wraps the board in an object: the MCP SDK requires a tool's
// output schema to be a JSON object, so bare slices must be wrapped.
type setHypothesesOut struct {
	Board []store.Hypothesis `json:"board"`
}

func setHypotheses(ctx context.Context, c *daemonClient, in setHypothesesIn) (setHypothesesOut, error) {
	board, err := c.setHypotheses(ctx, in.SessionID, in.Hypotheses)
	if err != nil {
		return setHypothesesOut{}, fmt.Errorf("set_hypotheses: %w", err)
	}
	return setHypothesesOut{Board: board}, nil
}

type updateHypothesisIn struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id" jsonschema:"the hypothesis ID, e.g. h2"`
	Status    string `json:"status" jsonschema:"active, killed, or confirmed"`
	Note      string `json:"note,omitempty" jsonschema:"evidence note explaining the status"`
}

func updateHypothesis(ctx context.Context, c *daemonClient, in updateHypothesisIn) (store.Hypothesis, error) {
	if !validStatus(in.Status) {
		return store.Hypothesis{}, fmt.Errorf("update_hypothesis: invalid status %q (want active, killed, or confirmed)", in.Status)
	}
	h, err := c.updateHypothesis(ctx, in.SessionID, in.ID, in.Status, in.Note)
	if err != nil {
		return store.Hypothesis{}, fmt.Errorf("update_hypothesis: %w", err)
	}
	return h, nil
}

// validStatus gates the hypothesis status enum client-side so a typo fails with a
// clear message before hitting the daemon, mirroring validVerdict (D6: the status
// enum is closed; the store accepts any string).
func validStatus(s string) bool {
	switch store.HypothesisStatus(s) {
	case store.HypothesisActive, store.HypothesisKilled, store.HypothesisConfirmed:
		return true
	default:
		return false
	}
}

type registerProbeIn struct {
	SessionID    string `json:"session_id"`
	ID           string `json:"id" jsonschema:"the probe ID used in its URL, e.g. p3"`
	File         string `json:"file" jsonschema:"source file the probe line sits in"`
	Line         int    `json:"line" jsonschema:"line number of the probe"`
	HypothesisID string `json:"hypothesis_id" jsonschema:"the hypothesis this probe discriminates"`
	Note         string `json:"note,omitempty"`
}

func registerProbe(ctx context.Context, c *daemonClient, in registerProbeIn) (store.Probe, error) {
	p := store.Probe{
		ID: in.ID, File: in.File, Line: in.Line,
		HypothesisID: in.HypothesisID, Note: in.Note,
	}
	saved, err := c.registerProbe(ctx, in.SessionID, p)
	if err != nil {
		return store.Probe{}, fmt.Errorf("register_probe: %w", err)
	}
	return saved, nil
}

type removeProbeIn struct {
	SessionID string `json:"session_id"`
	ID        string `json:"id" jsonschema:"the probe ID to mark removed"`
}

type removeProbeOut struct {
	Removed   bool   `json:"removed"`
	ProbeID   string `json:"probe_id"`
	SessionID string `json:"session_id"`
}

func removeProbe(ctx context.Context, c *daemonClient, in removeProbeIn) (removeProbeOut, error) {
	if err := c.removeProbe(ctx, in.SessionID, in.ID); err != nil {
		return removeProbeOut{}, fmt.Errorf("remove_probe: %w", err)
	}
	return removeProbeOut{Removed: true, ProbeID: in.ID, SessionID: in.SessionID}, nil
}

// --- runs + query (D9, 4.5) ---

type awaitRunIn struct {
	SessionID string `json:"session_id"`
	TimeoutS  int    `json:"timeout_s,omitempty" jsonschema:"max seconds to wait; defaults to 120"`
}

func awaitRun(ctx context.Context, c *daemonClient, in awaitRunIn) (awaitRunResult, error) {
	timeout := in.TimeoutS
	if timeout <= 0 {
		timeout = 120 // D8 default, mirrored client-side for explicitness
	}
	res, err := c.awaitRun(ctx, in.SessionID, timeout)
	if err != nil {
		return awaitRunResult{}, fmt.Errorf("await_run: %w", err)
	}
	return res, nil
}

type closeRunIn struct {
	SessionID string `json:"session_id"`
	Verdict   string `json:"verdict" jsonschema:"reproduced, not-reproduced, or fixed-check"`
}

func closeRun(ctx context.Context, c *daemonClient, in closeRunIn) (store.Run, error) {
	if !validVerdict(in.Verdict) {
		return store.Run{}, fmt.Errorf("close_run: invalid verdict %q (want reproduced, not-reproduced, or fixed-check)", in.Verdict)
	}
	run, err := c.closeRun(ctx, in.SessionID, in.Verdict)
	if err != nil {
		return store.Run{}, fmt.Errorf("close_run: %w", err)
	}
	return run, nil
}

// validVerdict gates the verdict enum client-side so a typo fails with a clear
// message before hitting the daemon (the store accepts any string).
func validVerdict(v string) bool {
	switch store.RunVerdict(v) {
	case store.VerdictReproduced, store.VerdictNotReproduced, store.VerdictFixedCheck:
		return true
	default:
		return false
	}
}

type queryLogsIn struct {
	SessionID string `json:"session_id"`
	Probe     string `json:"probe,omitempty" jsonschema:"limit to one probe ID"`
	Run       string `json:"run,omitempty" jsonschema:"limit to one run ID"`
	Limit     int    `json:"limit,omitempty" jsonschema:"cap events returned per bucket"`
}

type queryLogsOut struct {
	Results []store.QueryResult `json:"results"`
}

func queryLogs(ctx context.Context, c *daemonClient, in queryLogsIn) (queryLogsOut, error) {
	results, err := c.queryLogs(ctx, in.SessionID, store.QueryFilter{
		Run: in.Run, Probe: in.Probe, Limit: in.Limit,
	})
	if err != nil {
		return queryLogsOut{}, fmt.Errorf("query_logs: %w", err)
	}
	return queryLogsOut{Results: results}, nil
}

type diffRunsIn struct {
	RunA string `json:"run_a" jsonschema:"the first run ID to compare, e.g. 1"`
	RunB string `json:"run_b" jsonschema:"the second run ID to compare, e.g. 3"`
	// SessionID targets a specific investigation; omit to diff the latest open one,
	// matching the latest-or-named pattern of debug_resume and close_run.
	SessionID string `json:"session_id,omitempty" jsonschema:"the investigation; omit for the latest open one"`
}

// diffRuns compares two runs of one session (mcp-server spec: diff_runs tool).
// The tool's contract is per the active session (run_a, run_b); session_id is an
// optional override resolving to the latest open session otherwise, so a typical
// fix-confirmation call needs only the two run IDs.
func diffRuns(ctx context.Context, c *daemonClient, in diffRunsIn) (store.RunDiff, error) {
	sessionID := in.SessionID
	if sessionID == "" {
		sess, err := c.resumeLatest(ctx)
		if err != nil {
			return store.RunDiff{}, fmt.Errorf("diff_runs: %w", err)
		}
		sessionID = sess.ID
	}
	diff, err := c.diffRuns(ctx, sessionID, in.RunA, in.RunB)
	if err != nil {
		return store.RunDiff{}, fmt.Errorf("diff_runs: %w", err)
	}
	return diff, nil
}

// --- report_observation: the field-notes channel (field-notes D2/D3) ---

type reportObservationIn struct {
	Note      string `json:"note" jsonschema:"the observation about sherlog's own behavior"`
	Category  string `json:"category" jsonschema:"tool-bug, friction, anomaly, or other"`
	SessionID string `json:"session_id,omitempty" jsonschema:"the active investigation, when one is open"`
}

// reportObservationOut is the minimal acknowledgment (design D3): a boolean the
// skill ignores. Filing never blocks an investigation, so this is deliberately
// thin and always reports filed=true on success / filed=false on a swallowed
// failure, never a tool error.
type reportObservationOut struct {
	Filed bool `json:"filed"`
}

// addReportObservation registers the fire-and-forget field-notes tool with its own
// handler (not the generic add) so a filing failure — including the daemon being
// unreachable — is swallowed into a minimal acknowledgment rather than surfaced as
// a tool error (field-notes design D3: filing must never interrupt an
// investigation). The note records sherlog misbehavior for the maintainer and is
// never user-facing.
func addReportObservation(server *mcpsdk.Server, c *daemonClient) {
	mcpsdk.AddTool(server, &mcpsdk.Tool{
		Name: "report_observation",
		Description: "File a private field note when sherlog ITSELF behaves unexpectedly " +
			"(zero events despite a confirmed reproduction, await/debounce oddities, " +
			"cleanup-gate surprises, tool errors). Categories: tool-bug, friction, " +
			"anomaly, other. Fire-and-forget: it never blocks and is never shown to the " +
			"user — file it and continue the investigation. Difficulties with the user's " +
			"own bug are NOT observations; only sherlog's behavior is.",
	}, func(ctx context.Context, _ *mcpsdk.CallToolRequest, in reportObservationIn) (*mcpsdk.CallToolResult, reportObservationOut, error) {
		// Best-effort daemon availability and filing; any failure is swallowed so the
		// investigation is never interrupted (D3). No error is ever returned.
		if err := c.ensureDaemon(ctx); err != nil {
			return nil, reportObservationOut{Filed: false}, nil
		}
		if _, err := c.reportObservation(ctx, in.SessionID, in.Category, in.Note); err != nil {
			return nil, reportObservationOut{Filed: false}, nil
		}
		return nil, reportObservationOut{Filed: true}, nil
	})
}
