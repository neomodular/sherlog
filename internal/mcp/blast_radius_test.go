package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// The blast-radius suite covers the MCP tool half of add-blast-radius (mcp-server
// spec): map_blast_radius passes the agent's pattern to the daemon and returns the
// recorded radius (hits, truncation, unreviewed count); annotate_blast_radius validates
// the verdict enum client-side before the round-trip and relays the daemon's
// set-membership rejections verbatim. It drives the full client → daemon → store path
// against a live daemon over a temp-dir store — the same surface production uses (D2) —
// with a real working tree so the daemon actually walks files (never the repo).

const (
	// blastCulpritRel is the confirmed culprit file, cwd-relative so it resolves under
	// the session cwd exactly as the walker records it.
	blastCulpritRel = "pay/charge.go"
	// blastSiblingRel is a sibling call site carrying the same anti-pattern.
	blastSiblingRel = "pay/refund.go"
	// blastPattern matches the anti-pattern in both files; it also matches the culprit,
	// so it clears the false-coverage gate.
	blastPattern = `dangerousParse\(`
	// blastProbeLine is the line the anti-pattern sits on in each source file below.
	blastProbeLine = 4
)

// blastSource returns a tiny Go source file whose 4th line carries the dangerousParse
// anti-pattern, so a probe registered at line 4 points at a real matching line.
func blastSource(fn string) string {
	return "package pay\n\nfunc " + fn + "(amount string) int {\n\treturn dangerousParse(amount)\n}\n"
}

// writeBlastSource writes rel (a slash path) under dir, creating parent directories.
func writeBlastSource(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// newConfirmedBlastCase builds a real working tree containing the culprit and a sibling,
// chdirs into it so debug_start pins that cwd on the session, starts a live daemon, and
// drives the case to a confirmed hypothesis whose cited probe sits at the culprit's
// anti-pattern line. It returns the connected client session and the session ID — the
// exact precondition the false-coverage gate needs (D-C). t.Chdir restores the cwd on
// test cleanup.
func newConfirmedBlastCase(t *testing.T, ctx context.Context) (*mcpsdk.ClientSession, string) {
	t.Helper()
	dir := t.TempDir()
	writeBlastSource(t, dir, blastCulpritRel, blastSource("Charge"))
	writeBlastSource(t, dir, blastSiblingRel, blastSource("Refund"))
	t.Chdir(dir)

	base, port := startTestDaemon(t)
	sess := connectMCP(t, ctx, base, port)

	sid := startCase(t, ctx, sess, "totals truncate through dangerousParse")
	setThreeSuspects(t, ctx, sess, sid)
	// The confirming probe cites the culprit file relative, so it resolves under the
	// session cwd the same way the walker records its hits.
	callTool(t, ctx, sess, "register_probe", map[string]any{
		"session_id": sid, "id": "p1", "file": blastCulpritRel, "line": blastProbeLine,
		"hypothesis_id":    "h1",
		"expected_if_true": "dangerousParse truncates", "expected_if_false": "value parses cleanly",
	}, nil)
	run := seedClosedRun(t, ctx, sess, sid, "reproduced")
	callTool(t, ctx, sess, "update_hypothesis", map[string]any{
		"session_id": sid, "id": "h1", "status": "confirmed",
		"note": "probe showed truncation", "probe_id": "p1", "run_id": run.ID,
	}, nil)
	return sess, sid
}

// mapRadius runs map_blast_radius and returns the decoded result, failing on a tool
// error so a search that should have succeeded cannot pass silently.
func mapRadius(t *testing.T, ctx context.Context, sess *mcpsdk.ClientSession, sid, pattern string) blastRadiusResult {
	t.Helper()
	var res blastRadiusResult
	callTool(t, ctx, sess, "map_blast_radius", map[string]any{
		"session_id": sid, "pattern": pattern,
	}, &res)
	return res
}

// TestMapBlastRadiusTool covers the map_blast_radius requirement (mcp-server spec): a
// valid pattern covering the culprit returns every daemon-recorded hit with file, line,
// and excerpt plus the truncation flag, and a pattern that misses the culprit surfaces
// the daemon's gate rejection verbatim, naming that file.
func TestMapBlastRadiusTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("radius returned covering the culprit", func(t *testing.T) {
		sess, sid := newConfirmedBlastCase(t, ctx)

		res := mapRadius(t, ctx, sess, sid, blastPattern)
		if res.Pattern != blastPattern {
			t.Errorf("radius pattern = %q, want %q", res.Pattern, blastPattern)
		}
		if res.Truncated {
			t.Errorf("radius truncated for a two-hit search: %+v", res)
		}
		// Two files carry the anti-pattern; neither is graded yet, so both are unreviewed.
		if len(res.Hits) != 2 {
			t.Fatalf("hit count = %d, want 2: %+v", len(res.Hits), res.Hits)
		}
		if res.UnreviewedCount != 2 {
			t.Errorf("unreviewed count = %d, want 2 (nothing graded yet)", res.UnreviewedCount)
		}
		byFile := map[string]struct {
			line    int
			excerpt string
			verdict string
		}{}
		for _, h := range res.Hits {
			byFile[h.File] = struct {
				line    int
				excerpt string
				verdict string
			}{h.Line, h.Excerpt, string(h.Verdict)}
		}
		for _, want := range []string{blastCulpritRel, blastSiblingRel} {
			h, ok := byFile[want]
			if !ok {
				t.Fatalf("hit for %s missing from %v", want, byFile)
			}
			if h.line != blastProbeLine {
				t.Errorf("%s hit line = %d, want %d", want, h.line, blastProbeLine)
			}
			if !strings.Contains(h.excerpt, "dangerousParse") {
				t.Errorf("%s hit excerpt = %q, want it to carry the matched source", want, h.excerpt)
			}
			if h.verdict != "" {
				t.Errorf("%s hit should be unreviewed before annotation, got verdict %q", want, h.verdict)
			}
		}
	})

	t.Run("gate rejection surfaces verbatim", func(t *testing.T) {
		sess, sid := newConfirmedBlastCase(t, ctx)

		// A well-formed pattern that matches nothing: the culprit is absent from the
		// (empty) hit set, so the false-coverage gate (D-C) rejects the search.
		msg := callToolExpectErr(t, ctx, sess, "map_blast_radius", map[string]any{
			"session_id": sid, "pattern": `neverMatchesXyzzy\b`,
		})
		// The daemon's repair instruction names the culprit file; it must reach the model
		// unaltered so the agent broadens the pattern rather than fabricating coverage.
		if !strings.Contains(msg, blastCulpritRel) {
			t.Errorf("gate rejection should name the culprit file %q, got: %s", blastCulpritRel, msg)
		}
		if !strings.Contains(msg, "does not match the confirmed culprit") {
			t.Errorf("gate rejection should carry the daemon's message verbatim, got: %s", msg)
		}
	})

	t.Run("map before any confirm surfaces the daemon gate", func(t *testing.T) {
		// No confirmed hypothesis on the board: the gate (D-C) rejects before a walk can
		// manufacture coverage, and the daemon's instruction surfaces verbatim.
		dir := t.TempDir()
		writeBlastSource(t, dir, blastCulpritRel, blastSource("Charge"))
		t.Chdir(dir)
		base, port := startTestDaemon(t)
		sess := connectMCP(t, ctx, base, port)
		sid := startCase(t, ctx, sess, "no confirm yet")
		setThreeSuspects(t, ctx, sess, sid)

		msg := callToolExpectErr(t, ctx, sess, "map_blast_radius", map[string]any{
			"session_id": sid, "pattern": blastPattern,
		})
		if !strings.Contains(msg, "confirm a hypothesis") {
			t.Errorf("no-confirm rejection should carry the daemon's repair instruction, got: %s", msg)
		}
	})

	t.Run("uncompilable pattern surfaces the daemon compile error", func(t *testing.T) {
		sess, sid := newConfirmedBlastCase(t, ctx)
		msg := callToolExpectErr(t, ctx, sess, "map_blast_radius", map[string]any{
			"session_id": sid, "pattern": "(unterminated",
		})
		if !strings.Contains(msg, "failed to compile") {
			t.Errorf("bad regex should surface the daemon's compile error verbatim, got: %s", msg)
		}
	})
}

// TestAnnotateBlastRadiusTool covers the annotate_blast_radius requirement (mcp-server
// spec): the verdict enum is validated client-side before the round-trip, honest
// grades merge into the recorded radius with the unreviewed count updated, and the
// daemon's set-membership rejection of an unrecorded site surfaces verbatim.
func TestAnnotateBlastRadiusTool(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("verdict enum validated client-side", func(t *testing.T) {
		// A live daemon is reachable (ensureDaemon succeeds), but the verdict check runs
		// in the tool handler before any annotate round-trip, so a bogus session id never
		// matters for a rejected verdict — the error talks about the verdict, not a
		// missing session. A valid verdict, by contrast, passes the client gate and only
		// then meets the daemon.
		base, port := startTestDaemon(t)
		sess := connectMCP(t, ctx, base, port)

		cases := []struct {
			name    string
			verdict string
			reject  bool
		}{
			{"empty verdict", "", true},
			{"typo verdict", "probably-fine", true},
			{"wrong case", "Sibling-Bug", true},
			{"valid passes client gate", "safe", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				args := map[string]any{
					"session_id": "no-such-session",
					"annotations": []map[string]any{
						{"file": blastCulpritRel, "line": blastProbeLine, "verdict": tc.verdict},
					},
				}
				msg := callToolExpectErr(t, ctx, sess, "annotate_blast_radius", args)
				if tc.reject {
					// Rejected before the daemon: the message names the allowed set and never
					// mentions the (bogus) session, proving it failed at the client gate.
					if !strings.Contains(msg, "invalid verdict") || !strings.Contains(msg, "sibling-bug") {
						t.Errorf("client-side rejection should name the allowed verdicts, got: %s", msg)
					}
					if strings.Contains(msg, "session not found") {
						t.Errorf("rejection reached the daemon instead of failing client-side: %s", msg)
					}
					return
				}
				// A valid verdict clears the client gate and reaches the daemon, which then
				// rejects the unknown session — proof the enum check let it through.
				if strings.Contains(msg, "invalid verdict") {
					t.Errorf("a valid verdict must clear the client gate, got: %s", msg)
				}
				if !strings.Contains(msg, "session not found") {
					t.Errorf("a valid verdict should reach the daemon and hit the unknown session, got: %s", msg)
				}
			})
		}
	})

	t.Run("honest grades merge and unreviewed count updates", func(t *testing.T) {
		sess, sid := newConfirmedBlastCase(t, ctx)
		mapRadius(t, ctx, sess, sid, blastPattern) // two hits, both unreviewed

		// Grade only the sibling; the culprit stays unreviewed and must be reported so.
		var res blastRadiusResult
		callTool(t, ctx, sess, "annotate_blast_radius", map[string]any{
			"session_id": sid,
			"annotations": []map[string]any{
				{"file": blastSiblingRel, "line": blastProbeLine, "verdict": "sibling-bug", "note": "same truncation"},
			},
		}, &res)

		if res.UnreviewedCount != 1 {
			t.Errorf("unreviewed count = %d, want 1 (culprit still ungraded)", res.UnreviewedCount)
		}
		verdicts := map[string]string{}
		for _, h := range res.Hits {
			verdicts[h.File] = string(h.Verdict)
		}
		if verdicts[blastSiblingRel] != "sibling-bug" {
			t.Errorf("sibling verdict = %q, want sibling-bug", verdicts[blastSiblingRel])
		}
		if verdicts[blastCulpritRel] != "" {
			t.Errorf("culprit should stay unreviewed, got verdict %q", verdicts[blastCulpritRel])
		}
	})

	t.Run("unknown site rejected by the daemon verbatim", func(t *testing.T) {
		sess, sid := newConfirmedBlastCase(t, ctx)
		mapRadius(t, ctx, sess, sid, blastPattern)

		// A valid verdict on a line the search never recorded: the client gate passes it,
		// and the daemon's set-membership check rejects it, naming the site.
		msg := callToolExpectErr(t, ctx, sess, "annotate_blast_radius", map[string]any{
			"session_id": sid,
			"annotations": []map[string]any{
				{"file": blastSiblingRel, "line": 999, "verdict": "safe"},
			},
		})
		if !strings.Contains(msg, "no recorded hit") || !strings.Contains(msg, "999") {
			t.Errorf("daemon set-membership rejection should name the unrecorded site, got: %s", msg)
		}
	})
}
