// Package store defines the core investigation data model and (in later task
// groups) the in-memory store with JSONL/JSON persistence described in design
// decision D5. This file holds only the types shared across the daemon, MCP
// server, and CLI so they have a single source of truth (D6: the daemon owns
// investigation state).
package store

import "time"

// HypothesisStatus is the lifecycle state of a suspect on the board (D6).
type HypothesisStatus string

const (
	// HypothesisActive is a suspect still in play.
	HypothesisActive HypothesisStatus = "active"
	// HypothesisKilled is a suspect ruled out by evidence.
	HypothesisKilled HypothesisStatus = "killed"
	// HypothesisConfirmed is the suspect confirmed as the root cause.
	HypothesisConfirmed HypothesisStatus = "confirmed"
)

// RunVerdict is the user-supplied outcome that closes a run (D7).
type RunVerdict string

const (
	// VerdictReproduced means the bug was reproduced during the run.
	VerdictReproduced RunVerdict = "reproduced"
	// VerdictNotReproduced means the bug did not appear during the run.
	VerdictNotReproduced RunVerdict = "not-reproduced"
	// VerdictFixedCheck means the run verified a candidate fix.
	VerdictFixedCheck RunVerdict = "fixed-check"
)

// Session is a single investigation. It owns the hypothesis board, probe
// registry, and run history so the investigation survives context death and is
// reconstructable by debug_resume (D5, D6).
type Session struct {
	ID          string       `json:"id"`          // short random base36 token (D4)
	Description string       `json:"description"` // the bug being investigated
	CWD         string       `json:"cwd"`         // enables same-cwd open-session detection
	CreatedAt   time.Time    `json:"created_at"`
	ClosedAt    *time.Time   `json:"closed_at,omitempty"` // nil while the session is open
	Hypotheses  []Hypothesis `json:"hypotheses"`
	Probes      []Probe      `json:"probes"`
	Runs        []Run        `json:"runs"`
}

// Hypothesis is a suspect on the board with its current status and the evidence
// notes accumulated against it (D6).
type Hypothesis struct {
	ID        string           `json:"id"`
	Statement string           `json:"statement"`
	Status    HypothesisStatus `json:"status"`
	Note      string           `json:"note,omitempty"` // latest evidence note
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// Probe is a registry entry for one placed probe line. The Removed flag backs
// the cleanup guarantee: debug_end reports every probe whose flag is unset (D10).
type Probe struct {
	ID           string    `json:"id"`
	File         string    `json:"file"`
	Line         int       `json:"line"`
	HypothesisID string    `json:"hypothesis_id"` // the suspect this probe discriminates
	Note         string    `json:"note,omitempty"`
	Removed      bool      `json:"removed"` // set once the probe line is deleted from code
	CreatedAt    time.Time `json:"created_at"`
}

// Run is a first-class repro attempt (D7). await_run opens it; the user's
// verdict closes it. Every LogEvent is stamped with the run it belongs to so the
// skill can produce per-run probe summaries and post-MVP diff_runs.
type Run struct {
	ID        string     `json:"id"`
	StartedAt time.Time  `json:"started_at"`
	ClosedAt  *time.Time `json:"closed_at,omitempty"` // nil while the run is open
	Verdict   RunVerdict `json:"verdict,omitempty"`   // set when the run closes
}

// LogEvent is a single ingested probe hit. Body holds the parsed JSON value when
// the request body parsed as JSON; Raw holds the original string when it did not
// (D3: the daemon never rejects a body). Storage is bounded by flood control:
// first/last-N per probe per run (D8).
type LogEvent struct {
	TS    time.Time `json:"ts"`
	Run   string    `json:"run"`            // owning run ID
	Probe string    `json:"probe"`          // probe ID from the URL path
	Body  any       `json:"body,omitempty"` // parsed JSON, when parseable
	Raw   string    `json:"raw,omitempty"`  // original body when not JSON
}
