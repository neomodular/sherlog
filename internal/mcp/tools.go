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
		"Open a new debugging investigation. Provide a short, specific title (the case "+
			"identity, ≤60 chars) and a detailed bug_description. Returns the session ID, "+
			"the probe URL template, and fire-and-forget probe one-liners for JS/browser, "+
			"Python, Go, Ruby, and curl. Start every debug session here.",
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
			"pass root_cause, fix_summary, and confirmed_hypothesis_id (the id must name "+
			"a board-confirmed hypothesis) so it becomes recall material; optionally add "+
			"regression_test_ref and a guardrail (test/lint/alert/doc). Omit all "+
			"resolution fields to close unsolved.",
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
		"Update a hypothesis's status (active, killed, confirmed) with an evidence "+
			"note. A kill or confirm MUST cite probe_id and run_id — the probe and "+
			"closed run whose evidence justifies the verdict; a refine (active) needs "+
			"no citation. Confirm additionally requires a reproduced run and a cited "+
			"probe that carries predictions.",
		updateHypothesis)

	add(server, c, "register_probe",
		"Record a placed probe in the registry so cleanup is guaranteed findable: "+
			"its ID, file, line, and the hypothesis it discriminates. The file and line "+
			"must exist under the session cwd. Optionally supply expected_if_true and "+
			"expected_if_false (both or neither, and they must differ) to make it a "+
			"discriminating probe fit to confirm a root cause.",
		registerProbe)

	add(server, c, "remove_probe",
		"Mark a probe removed after its line has been deleted from the code.",
		removeProbe)

	add(server, c, "await_run",
		"Open (or re-attach to) a run and block until probe activity goes quiet or "+
			"the timeout elapses (default 120s). Re-invoke after a timeout to keep "+
			"waiting on the same run for long reproductions. Pass a prediction (how the "+
			"evidence should change if a candidate fix is right) before a fixed-check "+
			"run — it is required to later close that run fixed-check. Returns a "+
			"per-probe summary plus the session's repro rate, never raw logs.",
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

	add(server, c, "map_blast_radius",
		"After a hypothesis is CONFIRMED and BEFORE you apply the fix, hunt for sibling "+
			"occurrences of the same defect. Supply a regex pattern targeting the defect "+
			"MECHANISM (not the symptom prose); the daemon runs the search under the "+
			"session cwd and records every hit itself — you never supply the hit list. "+
			"The pattern MUST match the confirmed culprit's file or the search is rejected "+
			"(a pattern that misses the known bug proves nothing about siblings), so map it "+
			"while the anti-pattern still exists at the culprit site. Returns each hit "+
			"(file, line, excerpt), the truncation flag, and the unreviewed count. A re-run "+
			"replaces the previous radius and clears its annotations.",
		mapBlastRadius)

	add(server, c, "annotate_blast_radius",
		"Grade the sibling hits map_blast_radius recorded: for each, a verdict of "+
			"sibling-bug, safe, or already-covered, with an optional note. You may only "+
			"grade sites the search found — the daemon rejects any {file, line} not in the "+
			"recorded hits. Partial grading is fine; ungraded hits stay unreviewed and the "+
			"result reports the unreviewed count. Grade every hit honestly — safe is a "+
			"legitimate verdict; a site you cannot judge stays unreviewed.",
		annotateBlastRadius)

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
	// Title is the agent-authored case identity (add-case-titles D1/D4): a short,
	// specific summary of the failure (≤60 chars). Optional for backward
	// compatibility — an older caller that omits it still works, and the daemon
	// derives a fallback title from the description (mcp-server spec: Legacy caller).
	Title          string `json:"title,omitempty" jsonschema:"short specific case title (≤60 chars), e.g. 'Login 401 after idle timeout'"`
	BugDescription string `json:"bug_description" jsonschema:"the bug being investigated"`
}

type debugStartOut struct {
	SessionID string `json:"session_id"`
	// Title echoes the case identity the session was created with (mcp-server spec:
	// the response echoes it). Always non-empty: the daemon derives a fallback when
	// the caller omitted a title.
	Title string `json:"title"`
	// Commit is the repository commit SHA the daemon pinned on the session at creation
	// when the cwd is a git work tree (harden-detective-gates D-H), omitted otherwise.
	// Recording only — surfaced so the case is anchored to a known tree state.
	Commit        string         `json:"commit,omitempty"`
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
	res, err := c.createSession(ctx, in.Title, in.BugDescription, cwd)
	if err != nil {
		return debugStartOut{}, fmt.Errorf("debug_start: %w", err)
	}
	return debugStartOut{
		SessionID: res.Session.ID,
		// The daemon-side session carries the title (the supplied one, or the derived
		// fallback when omitted), so echo it from the response, never from the input.
		Title: res.Session.Title,
		// Commit is whatever the daemon pinned at creation (D-H): the HEAD SHA in a git
		// work tree, empty otherwise. Echoed from the session, never resolved here.
		Commit:        res.Session.Commit,
		ProbeContract: buildProbeContract(c.probeURLTemplate(res.Session.ID)),
		Preferences:   res.Preferences,
		WarnSameCWD:   res.ExistingSameCWD,
		RelatedCases:  res.RelatedCases,
	}, nil
}

type debugResumeIn struct {
	SessionID string `json:"session_id,omitempty" jsonschema:"resume this session; omit for the latest open one"`
}

// debugResume returns the reconstructed session state plus the computed repro rate
// with raw counts and the pinned commit (harden-detective-gates D-H, D-I), so a
// resumed investigation carries its determinism signal and tree anchor alongside the
// board, probe registry, and runs.
func debugResume(ctx context.Context, c *daemonClient, in debugResumeIn) (*sessionState, error) {
	var (
		sess *sessionState
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
	// Optional prevention references recorded with a solved close (harden-detective-
	// gates D-J): a regression test that now covers the bug, and a guardrail control.
	// Recorded and displayed, never fetched or executed (local-only invariant). Supply
	// only alongside a full resolution — a lone reference is a partial resolution the
	// daemon rejects.
	RegressionTestRef string       `json:"regression_test_ref,omitempty" jsonschema:"name/ref of a regression test that now covers the bug"`
	Guardrail         *guardrailIn `json:"guardrail,omitempty" jsonschema:"a prevention control: a test, lint, alert, or doc"`
}

// guardrailIn is the optional prevention control recorded on a solved close (D-J):
// a typed reference the daemon validates (type ∈ {test, lint, alert, doc}) and
// records, never executes. Ref is free text — a rule name, alert id, or doc path.
type guardrailIn struct {
	Type string `json:"type" jsonschema:"guardrail kind: test, lint, alert, or doc"`
	Ref  string `json:"ref,omitempty" jsonschema:"free-text reference: a rule name, alert id, or doc path"`
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
	// new fields preserves the prior debug_end behavior exactly. A prevention
	// reference alone counts as a resolution field (D-J): it is forwarded so the
	// daemon's solved-close gate (D-F) rejects the partial resolution rather than
	// letting it slip through as unsolved.
	var resolution *store.Resolution
	if in.RootCause != "" || in.FixSummary != "" || in.ConfirmedHypothesisID != "" ||
		in.RegressionTestRef != "" || in.Guardrail != nil {
		resolution = &store.Resolution{
			RootCause:             in.RootCause,
			FixSummary:            in.FixSummary,
			ConfirmedHypothesisID: in.ConfirmedHypothesisID,
			RegressionTestRef:     in.RegressionTestRef,
		}
		if in.Guardrail != nil {
			resolution.Guardrail = &store.Guardrail{Type: in.Guardrail.Type, Ref: in.Guardrail.Ref}
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
	// ProbeID and RunID cite the probe and closed run whose evidence justifies a kill
	// or confirm (harden-detective-gates D-B). Required client-side for killed/confirmed
	// (rejected before reaching the daemon); the daemon then cross-checks the citation
	// against its own registry. A refine (status active) needs neither.
	ProbeID string `json:"probe_id,omitempty" jsonschema:"probe whose evidence justifies a kill/confirm, e.g. p3"`
	RunID   string `json:"run_id,omitempty" jsonschema:"closed run whose evidence justifies a kill/confirm, e.g. r2"`
}

func updateHypothesis(ctx context.Context, c *daemonClient, in updateHypothesisIn) (store.Hypothesis, error) {
	if !validStatus(in.Status) {
		return store.Hypothesis{}, fmt.Errorf("update_hypothesis: invalid status %q (want active, killed, or confirmed)", in.Status)
	}
	// A kill or confirm must cite the probe_id and run_id whose evidence justifies the
	// verdict (D-B). Enforced client-side — mirroring the status-enum gate — so an
	// omitted citation fails with a clear message before reaching the daemon; the
	// daemon then cross-checks the citation it does receive.
	if requiresCitation(in.Status) && (in.ProbeID == "" || in.RunID == "") {
		return store.Hypothesis{}, fmt.Errorf("update_hypothesis: a %s verdict must cite probe_id and run_id — the probe and closed run whose evidence justifies it", in.Status)
	}
	h, err := c.updateHypothesis(ctx, in.SessionID, in.ID, in.Status, in.Note, in.ProbeID, in.RunID)
	if err != nil {
		return store.Hypothesis{}, fmt.Errorf("update_hypothesis: %w", err)
	}
	return h, nil
}

// requiresCitation reports whether a status transition must carry an evidence
// citation (D-B): killing or confirming a suspect does, refining it (active) does
// not. It gates the citation client-side the way validStatus gates the enum.
func requiresCitation(status string) bool {
	switch store.HypothesisStatus(status) {
	case store.HypothesisKilled, store.HypothesisConfirmed:
		return true
	default:
		return false
	}
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
	// ExpectedIfTrue and ExpectedIfFalse are the optional discriminating prediction
	// pair (harden-detective-gates D-A): how this probe's payload differs if its
	// hypothesis is true vs false. Supply both or neither, and they must differ; the
	// daemon validates the pair and echoes it back. A path tracer legitimately carries
	// neither, but the confirm gate only accepts a confirming probe that carries them.
	ExpectedIfTrue  string `json:"expected_if_true,omitempty" jsonschema:"what the probe shows if the hypothesis is true"`
	ExpectedIfFalse string `json:"expected_if_false,omitempty" jsonschema:"what the probe shows if the hypothesis is false"`
	Note            string `json:"note,omitempty"`
}

func registerProbe(ctx context.Context, c *daemonClient, in registerProbeIn) (store.Probe, error) {
	p := store.Probe{
		ID: in.ID, File: in.File, Line: in.Line,
		HypothesisID:    in.HypothesisID,
		ExpectedIfTrue:  in.ExpectedIfTrue,
		ExpectedIfFalse: in.ExpectedIfFalse,
		Note:            in.Note,
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
	// Prediction is the optional fix prediction (harden-detective-gates D-D): how the
	// evidence should change if the candidate fix is right. The daemon stamps it on the
	// run at call receipt — before any summary is returned — and it is immutable once
	// set. It is the prerequisite for a later fixed-check close, so the contrast is
	// judged against a recorded claim rather than conversation memory.
	Prediction string `json:"prediction,omitempty" jsonschema:"how the evidence should change if the fix is right (required before a fixed-check close)"`
}

func awaitRun(ctx context.Context, c *daemonClient, in awaitRunIn) (awaitRunResult, error) {
	timeout := in.TimeoutS
	if timeout <= 0 {
		timeout = 120 // D8 default, mirrored client-side for explicitness
	}
	res, err := c.awaitRun(ctx, in.SessionID, timeout, in.Prediction)
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
	RunA string `json:"run_a" jsonschema:"the first run ID to compare, e.g. r1"`
	RunB string `json:"run_b" jsonschema:"the second run ID to compare, e.g. r3"`
	// SessionID targets a specific investigation; omit to diff the latest open one,
	// matching the latest-or-named pattern of debug_resume and close_run.
	SessionID string `json:"session_id,omitempty" jsonschema:"the investigation; omit for the latest open one"`
}

// diffRuns compares two runs of one session (mcp-server spec: diff_runs tool).
// The tool's contract is per the active session (run_a, run_b); session_id is an
// optional override resolving to the latest open session otherwise, so a typical
// fix-confirmation call needs only the two run IDs.
func diffRuns(ctx context.Context, c *daemonClient, in diffRunsIn) (diffRunsResult, error) {
	sessionID := in.SessionID
	if sessionID == "" {
		sess, err := c.resumeLatest(ctx)
		if err != nil {
			return diffRunsResult{}, fmt.Errorf("diff_runs: %w", err)
		}
		sessionID = sess.ID
	}
	diff, err := c.diffRuns(ctx, sessionID, in.RunA, in.RunB)
	if err != nil {
		return diffRunsResult{}, fmt.Errorf("diff_runs: %w", err)
	}
	return diff, nil
}

// --- blast radius: sibling-occurrence search + annotation (add-blast-radius) ---

type mapBlastRadiusIn struct {
	SessionID string `json:"session_id"`
	// Pattern is the agent-authored regex the daemon executes (D-A): it targets the
	// defect mechanism and must match the confirmed culprit's file, or the daemon's
	// false-coverage gate rejects the search.
	Pattern string `json:"pattern" jsonschema:"regex targeting the defect mechanism; must match the confirmed culprit's file"`
	Note    string `json:"note,omitempty" jsonschema:"optional context recorded with the search"`
}

// mapBlastRadius is a pass-through to the daemon (D-A): the daemon compiles the
// pattern, walks the session cwd, and enforces the false-coverage gate. Every
// rejection — an empty or uncompilable pattern, no confirmed hypothesis, or a pattern
// that misses the culprit file — comes back as a daemon error the wrap surfaces
// verbatim, so the model can repair the pattern rather than fabricate coverage.
func mapBlastRadius(ctx context.Context, c *daemonClient, in mapBlastRadiusIn) (blastRadiusResult, error) {
	res, err := c.mapBlastRadius(ctx, in.SessionID, in.Pattern, in.Note)
	if err != nil {
		return blastRadiusResult{}, fmt.Errorf("map_blast_radius: %w", err)
	}
	return res, nil
}

type annotateBlastRadiusIn struct {
	SessionID   string                 `json:"session_id"`
	Annotations []blastAnnotationInput `json:"annotations" jsonschema:"per-hit verdicts to merge into the recorded radius"`
}

// blastAnnotationInput is one graded hit: the recorded {file, line} plus the agent's
// verdict and an optional note. The verdict is validated against the closed enum
// client-side before any daemon round-trip (D-D).
type blastAnnotationInput struct {
	File    string `json:"file" jsonschema:"the recorded hit's file, exactly as map_blast_radius returned it"`
	Line    int    `json:"line" jsonschema:"the recorded hit's line"`
	Verdict string `json:"verdict" jsonschema:"sibling-bug, safe, or already-covered"`
	Note    string `json:"note,omitempty" jsonschema:"optional rationale for the verdict"`
}

// annotateBlastRadius validates every verdict against the closed enum client-side
// (D-D) — mirroring the status/verdict gates — so a bad verdict fails with the allowed
// set named before reaching the daemon; the daemon still set-checks each {file, line}
// against the recorded hits it alone knows. A daemon rejection (unknown site, no
// radius) surfaces verbatim.
func annotateBlastRadius(ctx context.Context, c *daemonClient, in annotateBlastRadiusIn) (blastRadiusResult, error) {
	anns := make([]blastAnnotationBody, len(in.Annotations))
	for i, a := range in.Annotations {
		if !validBlastVerdict(a.Verdict) {
			return blastRadiusResult{}, fmt.Errorf("annotate_blast_radius: invalid verdict %q (want sibling-bug, safe, or already-covered)", a.Verdict)
		}
		anns[i] = blastAnnotationBody{File: a.File, Line: a.Line, Verdict: a.Verdict, Note: a.Note}
	}
	res, err := c.annotateBlastRadius(ctx, in.SessionID, anns)
	if err != nil {
		return blastRadiusResult{}, fmt.Errorf("annotate_blast_radius: %w", err)
	}
	return res, nil
}

// validBlastVerdict gates the blast-radius verdict enum client-side so a typo fails
// with a clear message before hitting the daemon (D-D), mirroring validStatus and
// validVerdict. The store re-validates authoritatively; this is the fast, actionable
// front door.
func validBlastVerdict(v string) bool {
	switch store.BlastVerdict(v) {
	case store.BlastSiblingBug, store.BlastSafe, store.BlastAlreadyCovered:
		return true
	default:
		return false
	}
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
