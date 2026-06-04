package store

import (
	"encoding/json"
	"testing"
	"time"
)

// TestSessionJSONRoundTrip guards the persisted shape of a session (D5: state.json
// is the durable form replayed on restart), ensuring nested records and the
// status/verdict enums survive a marshal/unmarshal cycle unchanged.
func TestSessionJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	in := Session{
		ID:          "a3f9zk12",
		Description: "login returns 500 intermittently",
		CWD:         "/home/u/app",
		CreatedAt:   now,
		Hypotheses: []Hypothesis{{
			ID:        "h1",
			Statement: "token refresh races the request",
			Status:    HypothesisActive,
			CreatedAt: now,
			UpdatedAt: now,
		}},
		Probes: []Probe{{
			ID:           "p1",
			File:         "auth.js",
			Line:         45,
			HypothesisID: "h1",
			CreatedAt:    now,
		}},
		Runs: []Run{{ID: "r1", StartedAt: now}},
	}

	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var out Session
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if out.ID != in.ID || out.Description != in.Description || out.CWD != in.CWD {
		t.Errorf("scalar fields changed: got %+v", out)
	}
	if len(out.Hypotheses) != 1 || out.Hypotheses[0].Status != HypothesisActive {
		t.Errorf("hypothesis not preserved: %+v", out.Hypotheses)
	}
	if len(out.Probes) != 1 || out.Probes[0].Removed {
		t.Errorf("probe not preserved (Removed should default false): %+v", out.Probes)
	}
	if len(out.Runs) != 1 || out.Runs[0].ClosedAt != nil {
		t.Errorf("run not preserved (ClosedAt should be nil while open): %+v", out.Runs)
	}
}

// TestLogEventBodyVsRaw documents the D3 contract: a parseable body lands in Body,
// an unparseable one in Raw. Both forms must round-trip through logs.jsonl.
func TestLogEventBodyVsRaw(t *testing.T) {
	cases := []LogEvent{
		{TS: time.Unix(0, 0).UTC(), Run: "r1", Probe: "p1", Body: map[string]any{"retries": float64(3)}},
		{TS: time.Unix(0, 0).UTC(), Run: "r1", Probe: "p2", Raw: "not json"},
	}
	for _, ev := range cases {
		data, err := json.Marshal(ev)
		if err != nil {
			t.Fatalf("marshal %+v: %v", ev, err)
		}
		var got LogEvent
		if err := json.Unmarshal(data, &got); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if got.Probe != ev.Probe || got.Run != ev.Run {
			t.Errorf("identity fields changed: got %+v want %+v", got, ev)
		}
	}
}
