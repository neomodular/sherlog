package store

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// This file covers the harden-detective-gates delta spec (specs/session-state):
// every scenario there is exercised here as an acceptance test against the store.

// --- Probes carry discriminating predictions (D-A) ---

// TestPredictionPairRoundTrips covers "Valid prediction pair round-trips".
func TestPredictionPairRoundTrips(t *testing.T) {
	root := t.TempDir()
	s := newTestStore(t, WithRoot(root))
	sess, _, _ := s.CreateSession("", "auth", "/repo")

	saved, err := s.RegisterProbe(sess.ID, Probe{
		ID: "p1", File: "auth.go", Line: 10, HypothesisID: "h1",
		ExpectedIfTrue:  "token=null past TTL",
		ExpectedIfFalse: "token populated, fresh",
	})
	if err != nil {
		t.Fatalf("RegisterProbe: %v", err)
	}
	if saved.ExpectedIfTrue != "token=null past TTL" || saved.ExpectedIfFalse != "token populated, fresh" {
		t.Errorf("prediction pair not echoed: %+v", saved)
	}

	// Survive a daemon restart over the same root.
	s2, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	got, err := s2.GetSession(sess.ID)
	if err != nil {
		t.Fatalf("GetSession after restart: %v", err)
	}
	if len(got.Probes) != 1 || got.Probes[0].ExpectedIfTrue != "token=null past TTL" || got.Probes[0].ExpectedIfFalse != "token populated, fresh" {
		t.Errorf("prediction pair not persisted across restart: %+v", got.Probes)
	}
}

// TestPredictionPairRejections covers "Non-discriminating pair rejected" and
// "One-sided prediction rejected".
func TestPredictionPairRejections(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")

	tests := []struct {
		name       string
		ifTrue     string
		ifFalse    string
		wantErr    error
		wantSubstr string
	}{
		{"equal modulo case and whitespace", "Token Missing", "  token missing ", ErrPredictionPair, "identical"},
		{"only if-true supplied", "fires", "", ErrPredictionPair, "expected_if_false"},
		{"only if-false supplied", "", "silent", ErrPredictionPair, "expected_if_true"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := s.RegisterProbe(sess.ID, Probe{
				ID: "p1", File: "a.go", Line: 1, HypothesisID: "h1",
				ExpectedIfTrue: tc.ifTrue, ExpectedIfFalse: tc.ifFalse,
			})
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("want %v, got %v", tc.wantErr, err)
			}
			if !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error should mention %q: %v", tc.wantSubstr, err)
			}
		})
	}
	// The rejected probes must not have been stored.
	got, _ := s.GetSession(sess.ID)
	if len(got.Probes) != 0 {
		t.Errorf("rejected probes must not persist: %+v", got.Probes)
	}
}

// --- Evidence citations on kill/confirm (D-B) ---

// closeReproRun opens and closes a run with the given verdict, returning its ID.
// Fixed-check verdicts require a prediction, so this helper is only for
// reproduced/not-reproduced runs.
func closeReproRun(t *testing.T, s *Store, sessionID string, verdict RunVerdict) string {
	t.Helper()
	run, err := s.OpenRun(sessionID)
	if err != nil {
		t.Fatalf("OpenRun: %v", err)
	}
	if _, err := s.CloseRun(sessionID, run.ID, verdict); err != nil {
		t.Fatalf("CloseRun(%s): %v", verdict, err)
	}
	return run.ID
}

// TestKillCitingZeroFiredProbeAccepted covers "Kill citing a zero-fired probe
// accepted": a probe that fired zero times is valid evidence.
func TestKillCitingZeroFiredProbeAccepted(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	s.SetHypotheses(sess.ID, []string{"race", "cache stale", "config drift"})
	if _, err := s.RegisterProbe(sess.ID, Probe{ID: "p4", File: "c.go", Line: 3, HypothesisID: "h2"}); err != nil {
		t.Fatalf("RegisterProbe: %v", err)
	}
	// r2 closes with a verdict; p4 never fired in it (total 0).
	runID := closeReproRun(t, s, sess.ID, VerdictReproduced)

	got, err := s.UpdateHypothesisWithEvidence(sess.ID, "h2", HypothesisKilled, "p4 silent", "p4", runID)
	if err != nil {
		t.Fatalf("kill citing zero-fired probe should succeed: %v", err)
	}
	if got.Status != HypothesisKilled || got.EvidenceProbeID != "p4" || got.EvidenceRunID != runID {
		t.Errorf("citation not recorded: %+v", got)
	}
}

// TestCitationRejections covers "Citation to an unknown run rejected", "Citation to
// an open run rejected", and "Kill without a citation rejected".
func TestCitationRejections(t *testing.T) {
	t.Run("unknown run", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		s.RegisterProbe(sess.ID, Probe{ID: "p4", File: "f", Line: 1, HypothesisID: "h2"})

		_, err := s.UpdateHypothesisWithEvidence(sess.ID, "h2", HypothesisKilled, "n", "p4", "r9")
		if !errors.Is(err, ErrRunNotFound) {
			t.Fatalf("want ErrRunNotFound, got %v", err)
		}
		if !strings.Contains(err.Error(), "r9") {
			t.Errorf("error should name the unknown run r9: %v", err)
		}
		got, _ := s.GetSession(sess.ID)
		if got.Hypotheses[1].Status != HypothesisActive {
			t.Errorf("hypothesis status must be unchanged on rejection: %+v", got.Hypotheses[1])
		}
	})

	t.Run("open run", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		s.RegisterProbe(sess.ID, Probe{
			ID: "p1", File: "f", Line: 1, HypothesisID: "h1",
			ExpectedIfTrue: "fires", ExpectedIfFalse: "silent",
		})
		run, _ := s.OpenRun(sess.ID) // left open, no verdict

		_, err := s.UpdateHypothesisWithEvidence(sess.ID, "h1", HypothesisConfirmed, "n", "p1", run.ID)
		if !errors.Is(err, ErrCitedRunNotClosed) {
			t.Fatalf("want ErrCitedRunNotClosed, got %v", err)
		}
		if !strings.Contains(err.Error(), "verdict") {
			t.Errorf("error should instruct recording the verdict first: %v", err)
		}
	})

	t.Run("missing citation", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})

		// The no-citation wrapper supplies empty probe/run: a kill is rejected.
		_, err := s.UpdateHypothesis(sess.ID, "h1", HypothesisKilled, "n")
		if !errors.Is(err, ErrEvidenceCitationRequired) {
			t.Fatalf("want ErrEvidenceCitationRequired, got %v", err)
		}
	})
}

// TestActiveTransitionExemptFromCitation covers the "Transitions to active require
// no citation" clause: a refine needs no probe/run.
func TestActiveTransitionExemptFromCitation(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	s.SetHypotheses(sess.ID, []string{"a", "b", "c"})

	got, err := s.UpdateHypothesis(sess.ID, "h1", HypothesisActive, "still in play")
	if err != nil {
		t.Fatalf("active transition should need no citation: %v", err)
	}
	if got.Status != HypothesisActive || got.Note != "still in play" {
		t.Errorf("refine not applied: %+v", got)
	}
}

// --- Confirm gate (D-C) ---

// TestConfirmGate covers "Confirm without any reproduced run rejected", "Confirm
// citing a prediction-less probe rejected", and "Fully qualified confirm succeeds".
func TestConfirmGate(t *testing.T) {
	t.Run("no reproduced run", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		s.RegisterProbe(sess.ID, Probe{
			ID: "p1", File: "f", Line: 1, HypothesisID: "h1",
			ExpectedIfTrue: "fires", ExpectedIfFalse: "silent",
		})
		// Only a not-reproduced run is closed — no reproduced run in the session.
		runID := closeReproRun(t, s, sess.ID, VerdictNotReproduced)

		_, err := s.UpdateHypothesisWithEvidence(sess.ID, "h1", HypothesisConfirmed, "n", "p1", runID)
		if !errors.Is(err, ErrNoReproducedRun) {
			t.Fatalf("want ErrNoReproducedRun, got %v", err)
		}
	})

	t.Run("prediction-less probe", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		// Probe carries no prediction pair.
		s.RegisterProbe(sess.ID, Probe{ID: "p1", File: "f", Line: 1, HypothesisID: "h1"})
		runID := closeReproRun(t, s, sess.ID, VerdictReproduced)

		_, err := s.UpdateHypothesisWithEvidence(sess.ID, "h1", HypothesisConfirmed, "n", "p1", runID)
		if !errors.Is(err, ErrCitedProbeUnpredicted) {
			t.Fatalf("want ErrCitedProbeUnpredicted, got %v", err)
		}
		if !strings.Contains(err.Error(), "expected_if_true") {
			t.Errorf("error should name the missing predictions: %v", err)
		}
	})

	t.Run("fully qualified", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		s.RegisterProbe(sess.ID, Probe{
			ID: "p1", File: "f", Line: 1, HypothesisID: "h1",
			ExpectedIfTrue: "fires", ExpectedIfFalse: "silent",
		})
		runID := closeReproRun(t, s, sess.ID, VerdictReproduced)

		got, err := s.UpdateHypothesisWithEvidence(sess.ID, "h1", HypothesisConfirmed, "root cause", "p1", runID)
		if err != nil {
			t.Fatalf("fully qualified confirm should succeed: %v", err)
		}
		if got.Status != HypothesisConfirmed || got.EvidenceProbeID != "p1" || got.EvidenceRunID != runID {
			t.Errorf("confirm citation not persisted: %+v", got)
		}
	})
}

// --- Board minimum (D-E) ---

// TestBoardMinimumEnforced covers "Two-suspect board rejected".
func TestBoardMinimumEnforced(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	// Seed a valid board first so we can prove a rejected resubmit leaves it intact.
	if _, err := s.SetHypotheses(sess.ID, []string{"a", "b", "c"}); err != nil {
		t.Fatalf("seed board: %v", err)
	}

	_, err := s.SetHypotheses(sess.ID, []string{"only", "two"})
	if !errors.Is(err, ErrInsufficientHypotheses) {
		t.Fatalf("want ErrInsufficientHypotheses, got %v", err)
	}
	got, _ := s.GetSession(sess.ID)
	if len(got.Hypotheses) != 3 {
		t.Errorf("existing board must be unchanged on rejection: %+v", got.Hypotheses)
	}
}

// --- Run fix predictions (D-D) ---

// TestFixedCheckWithoutPredictionRejected covers "Fixed-check without a prediction
// rejected" for both close paths.
func TestFixedCheckWithoutPredictionRejected(t *testing.T) {
	t.Run("CloseRun", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		run, _ := s.OpenRun(sess.ID) // no prediction
		_, err := s.CloseRun(sess.ID, run.ID, VerdictFixedCheck)
		if !errors.Is(err, ErrFixedCheckNeedsPrediction) {
			t.Fatalf("want ErrFixedCheckNeedsPrediction, got %v", err)
		}
		if !strings.Contains(err.Error(), "re-await") {
			t.Errorf("error should instruct re-awaiting with a prediction: %v", err)
		}
		// The run stays open for a re-await.
		if _, ok, _ := s.LatestOpenRun(sess.ID); !ok {
			t.Errorf("rejected fixed-check must leave the run open")
		}
	})

	t.Run("CloseLatestOpenRun", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.OpenRun(sess.ID) // no prediction
		_, err := s.CloseLatestOpenRun(sess.ID, VerdictFixedCheck)
		if !errors.Is(err, ErrFixedCheckNeedsPrediction) {
			t.Fatalf("want ErrFixedCheckNeedsPrediction, got %v", err)
		}
	})
}

// TestPredictionImmutableOnceSet covers "Prediction is immutable once set".
func TestPredictionImmutableOnceSet(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")

	first, err := s.OpenOrAttachRunWithPrediction(sess.ID, "p1 populated after fix")
	if err != nil {
		t.Fatalf("open with prediction: %v", err)
	}
	if first.Prediction != "p1 populated after fix" || first.PredictionAt == nil {
		t.Fatalf("prediction not stamped at open: %+v", first)
	}

	// Re-attach with a different prediction: the stored one must not change.
	second, err := s.OpenOrAttachRunWithPrediction(sess.ID, "a totally different claim")
	if err != nil {
		t.Fatalf("re-attach: %v", err)
	}
	if second.ID != first.ID {
		t.Fatalf("re-attach opened a new run: %s vs %s", second.ID, first.ID)
	}
	if second.Prediction != "p1 populated after fix" {
		t.Errorf("prediction overwritten on re-attach: %q", second.Prediction)
	}
	if second.PredictionAt == nil || !second.PredictionAt.Equal(*first.PredictionAt) {
		t.Errorf("prediction timestamp changed on re-attach: %v vs %v", second.PredictionAt, first.PredictionAt)
	}
}

// TestReattachStampsPredictionWhenAbsent covers the D-D clause "supplying it on a
// re-attach whose run has none is accepted".
func TestReattachStampsPredictionWhenAbsent(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")

	opened, _ := s.OpenRun(sess.ID) // opened with no prediction
	attached, err := s.OpenOrAttachRunWithPrediction(sess.ID, "p1 quiet after fix")
	if err != nil {
		t.Fatalf("re-attach with prediction: %v", err)
	}
	if attached.ID != opened.ID {
		t.Fatalf("re-attach opened a new run: %s vs %s", attached.ID, opened.ID)
	}
	if attached.Prediction != "p1 quiet after fix" || attached.PredictionAt == nil {
		t.Errorf("prediction not stamped on a run that had none: %+v", attached)
	}
}

// TestPredictedFixedCheckCloses covers "Predicted fixed-check closes", including
// persistence across a restart.
func TestPredictedFixedCheckCloses(t *testing.T) {
	root := t.TempDir()
	s := newTestStore(t, WithRoot(root))
	sess, _, _ := s.CreateSession("", "bug", "/repo")

	run, _ := s.OpenOrAttachRunWithPrediction(sess.ID, "p1 token now populated; p5 fires zero times")
	closed, err := s.CloseRun(sess.ID, run.ID, VerdictFixedCheck)
	if err != nil {
		t.Fatalf("predicted fixed-check close should succeed: %v", err)
	}
	if closed.Verdict != VerdictFixedCheck || closed.Prediction == "" {
		t.Errorf("closed run lost verdict/prediction: %+v", closed)
	}

	s2, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	got, _ := s2.GetSession(sess.ID)
	if len(got.Runs) != 1 || got.Runs[0].Verdict != VerdictFixedCheck || got.Runs[0].Prediction != "p1 token now populated; p5 fires zero times" {
		t.Errorf("prediction/verdict not recovered: %+v", got.Runs)
	}
}

// --- Commit pinning (D-H) ---

// initGitRepo builds a throwaway git repository with one commit and returns its
// path and HEAD SHA. The whole test skips when git is unavailable so the suite
// never assumes the environment is a repository.
func initGitRepo(t *testing.T) (dir, headSHA string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH; skipping repository commit test")
	}
	dir = t.TempDir()
	env := append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@example.com",
		"GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_CONFIG_SYSTEM="+os.DevNull,
	)
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	run("add", "f.txt")
	run("-c", "commit.gpgsign=false", "commit", "-q", "-m", "init")

	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = dir
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return dir, strings.TrimSpace(string(out))
}

// TestGitCommitResolvesRepoHead covers "Commit recorded in a git repository" at the
// resolver level.
func TestGitCommitResolvesRepoHead(t *testing.T) {
	dir, head := initGitRepo(t)
	got := gitCommit(dir)
	if got != head {
		t.Errorf("gitCommit(%q)=%q, want HEAD %q", dir, got, head)
	}
	if !looksLikeSHA(got) {
		t.Errorf("resolved commit is not a SHA: %q", got)
	}
}

// TestGitCommitNonRepoTolerated covers "Non-repository cwd tolerated" at the
// resolver level: a non-repo dir and an empty cwd both yield "".
func TestGitCommitNonRepoTolerated(t *testing.T) {
	if got := gitCommit(t.TempDir()); got != "" {
		t.Errorf("non-repo cwd should resolve to empty, got %q", got)
	}
	if got := gitCommit(""); got != "" {
		t.Errorf("empty cwd should resolve to empty, got %q", got)
	}
}

// TestCreateSessionPinsCommit covers commit capture at session creation with a
// deterministic resolver, including restart persistence and the empty-on-non-repo
// path — no dependency on the test environment being a repository.
func TestCreateSessionPinsCommit(t *testing.T) {
	root := t.TempDir()
	const sha = "0a1b2c3d4e5f6071"
	resolver := func(cwd string) string {
		if cwd == "/repo/pinned" {
			return sha
		}
		return ""
	}
	s, err := New(WithRoot(root), WithCommitResolver(resolver))
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	pinned, _, _ := s.CreateSession("", "bug", "/repo/pinned")
	if pinned.Commit != sha {
		t.Errorf("commit not pinned: %q want %q", pinned.Commit, sha)
	}
	bare, _, _ := s.CreateSession("", "bug", "/repo/no-git")
	if bare.Commit != "" {
		t.Errorf("non-repo session must have empty commit, got %q", bare.Commit)
	}

	// The pinned commit survives a restart.
	s2, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("restart New: %v", err)
	}
	got, _ := s2.GetSession(pinned.ID)
	if got.Commit != sha {
		t.Errorf("commit not recovered after restart: %q", got.Commit)
	}
}

// TestCreateSessionCommitFromRealRepo covers "Commit recorded in a git repository"
// end-to-end through the default resolver.
func TestCreateSessionCommitFromRealRepo(t *testing.T) {
	dir, head := initGitRepo(t)
	s, err := New(WithRoot(t.TempDir())) // default gitCommit resolver
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	sess, _, err := s.CreateSession("", "bug", dir)
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if sess.Commit != head {
		t.Errorf("session commit %q, want repo HEAD %q", sess.Commit, head)
	}
}

// --- Repro rate (D-I) ---

// TestReproRateFromVerdicts covers "Rate reflects verdicts": 2 reproduced, 1
// not-reproduced, and a fixed-check excluded → 2/3.
func TestReproRateFromVerdicts(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "flaky", "/repo")

	closeReproRun(t, s, sess.ID, VerdictReproduced)
	closeReproRun(t, s, sess.ID, VerdictNotReproduced)
	closeReproRun(t, s, sess.ID, VerdictReproduced)
	// A fixed-check run (needs a prediction) is excluded from the denominator.
	fc, _ := s.OpenOrAttachRunWithPrediction(sess.ID, "fix silences the probe")
	if _, err := s.CloseRun(sess.ID, fc.ID, VerdictFixedCheck); err != nil {
		t.Fatalf("close fixed-check: %v", err)
	}

	rr, err := s.ReproRate(sess.ID)
	if err != nil {
		t.Fatalf("ReproRate: %v", err)
	}
	if rr.Reproduced != 2 || rr.NotReproduced != 1 {
		t.Errorf("counts wrong: %+v", rr)
	}
	if rr.Rate < 0.66 || rr.Rate > 0.67 {
		t.Errorf("rate should be ~2/3, got %v", rr.Rate)
	}

	// An open run contributes nothing; an empty session reports a zero rate.
	empty, _, _ := s.CreateSession("", "fresh", "/repo/fresh")
	if rr0, _ := s.ReproRate(empty.ID); rr0.Rate != 0 || rr0.Reproduced != 0 || rr0.NotReproduced != 0 {
		t.Errorf("empty session should report a zero rate: %+v", rr0)
	}

	// The rate is derived, never stored: the persisted state carries no rate field.
	raw, err := os.ReadFile(filepath.Join(s.sessionDir(sess.ID), stateFile))
	if err != nil {
		t.Fatalf("read state: %v", err)
	}
	if strings.Contains(string(raw), "\"rate\"") || strings.Contains(string(raw), "repro_rate") {
		t.Errorf("repro rate must not be persisted: %s", raw)
	}
}

// --- Solved close validation (D-F) ---

// TestSolvedCloseValidation covers "Resolution naming an unconfirmed hypothesis
// rejected", "Partial resolution rejected", and "Unsolved close unaffected".
func TestSolvedCloseValidation(t *testing.T) {
	t.Run("unconfirmed hypothesis", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"}) // all active

		_, err := s.CloseSessionWithResolution(sess.ID, &Resolution{
			RootCause: "x", FixSummary: "y", ConfirmedHypothesisID: "h3",
		})
		if !errors.Is(err, ErrUnconfirmedHypothesis) {
			t.Fatalf("want ErrUnconfirmedHypothesis, got %v", err)
		}
		if !strings.Contains(err.Error(), "h3") {
			t.Errorf("error should name the unconfirmed hypothesis: %v", err)
		}
		// Rejection leaves the session OPEN (no silent unsolved downgrade).
		got, _ := s.GetSession(sess.ID)
		if got.ClosedAt != nil || got.Resolution != nil {
			t.Errorf("rejected solved close must leave the session open: closed=%v res=%+v", got.ClosedAt, got.Resolution)
		}
	})

	t.Run("partial resolution", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})

		_, err := s.CloseSessionWithResolution(sess.ID, &Resolution{RootCause: "only root cause"})
		if !errors.Is(err, ErrResolutionIncomplete) {
			t.Fatalf("want ErrResolutionIncomplete, got %v", err)
		}
		got, _ := s.GetSession(sess.ID)
		if got.ClosedAt != nil {
			t.Errorf("incomplete resolution must not close the session: %v", got.ClosedAt)
		}
	})

	t.Run("unsolved close unaffected", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		if _, err := s.CloseSession(sess.ID); err != nil {
			t.Fatalf("unsolved close: %v", err)
		}
		got, _ := s.GetSession(sess.ID)
		if got.ClosedAt == nil || got.Resolution != nil {
			t.Errorf("unsolved close should record no resolution: closed=%v res=%+v", got.ClosedAt, got.Resolution)
		}
	})
}

// --- Prevention references (D-J) ---

// TestGuardrailRecordedAndValidated covers "Guardrail recorded" and "Unknown
// guardrail type rejected".
func TestGuardrailRecordedAndValidated(t *testing.T) {
	t.Run("recorded", func(t *testing.T) {
		root := t.TempDir()
		s := newTestStore(t, WithRoot(root))
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		confirmH1(t, s, sess.ID)

		_, err := s.CloseSessionWithResolution(sess.ID, &Resolution{
			RootCause:             "root",
			FixSummary:            "fix",
			ConfirmedHypothesisID: "h1",
			RegressionTestRef:     "tests/regression/no_floating_refresh_test.go",
			Guardrail:             &Guardrail{Type: "lint", Ref: "eslint rule no-floating-refresh"},
		})
		if err != nil {
			t.Fatalf("guardrail close should succeed: %v", err)
		}

		// Persists and survives a restart.
		s2, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
		if err != nil {
			t.Fatalf("restart New: %v", err)
		}
		got, _ := s2.GetSession(sess.ID)
		if got.Resolution == nil || got.Resolution.Guardrail == nil {
			t.Fatalf("guardrail not recovered: %+v", got.Resolution)
		}
		if got.Resolution.Guardrail.Type != "lint" || got.Resolution.Guardrail.Ref != "eslint rule no-floating-refresh" {
			t.Errorf("guardrail fields lost: %+v", got.Resolution.Guardrail)
		}
		if got.Resolution.RegressionTestRef != "tests/regression/no_floating_refresh_test.go" {
			t.Errorf("regression test ref lost: %q", got.Resolution.RegressionTestRef)
		}
	})

	t.Run("unknown type rejected", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		confirmH1(t, s, sess.ID)

		_, err := s.CloseSessionWithResolution(sess.ID, &Resolution{
			RootCause: "root", FixSummary: "fix", ConfirmedHypothesisID: "h1",
			Guardrail: &Guardrail{Type: "vibes", Ref: "trust me"},
		})
		if !errors.Is(err, ErrInvalidGuardrailType) {
			t.Fatalf("want ErrInvalidGuardrailType, got %v", err)
		}
		if !strings.Contains(err.Error(), "test, lint, alert, doc") {
			t.Errorf("error should name the allowed types: %v", err)
		}
		got, _ := s.GetSession(sess.ID)
		if got.ClosedAt != nil {
			t.Errorf("invalid guardrail must not close the session: %v", got.ClosedAt)
		}
	})
}

// --- Legacy state (Migration Plan) ---

// TestNewFieldsJSONRoundTrip covers task 1.1: every new field survives a
// marshal/unmarshal cycle.
func TestNewFieldsJSONRoundTrip(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	in := Session{
		ID:          "s1",
		Commit:      "abc1234def",
		Description: "d",
		CWD:         "/repo",
		CreatedAt:   now,
		Hypotheses: []Hypothesis{{
			ID: "h1", Statement: "s", Status: HypothesisConfirmed,
			EvidenceProbeID: "p1", EvidenceRunID: "r1",
			CreatedAt: now, UpdatedAt: now,
		}},
		Probes: []Probe{{
			ID: "p1", File: "f", Line: 1, HypothesisID: "h1",
			ExpectedIfTrue: "t", ExpectedIfFalse: "f", CreatedAt: now,
		}},
		Runs: []Run{{
			ID: "r1", StartedAt: now, ClosedAt: &now, Verdict: VerdictFixedCheck,
			Prediction: "pred", PredictionAt: &now,
		}},
		Resolution: &Resolution{
			RootCause: "rc", FixSummary: "fs", ConfirmedHypothesisID: "h1", ClosedAt: now,
			RegressionTestRef: "t/ref", Guardrail: &Guardrail{Type: "doc", Ref: "runbook"},
		},
	}
	data, err := json.Marshal(in)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out Session
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out.Commit != "abc1234def" {
		t.Errorf("commit lost: %q", out.Commit)
	}
	if out.Hypotheses[0].EvidenceProbeID != "p1" || out.Hypotheses[0].EvidenceRunID != "r1" {
		t.Errorf("hypothesis citation lost: %+v", out.Hypotheses[0])
	}
	if out.Probes[0].ExpectedIfTrue != "t" || out.Probes[0].ExpectedIfFalse != "f" {
		t.Errorf("probe predictions lost: %+v", out.Probes[0])
	}
	if out.Runs[0].Prediction != "pred" || out.Runs[0].PredictionAt == nil {
		t.Errorf("run prediction lost: %+v", out.Runs[0])
	}
	if out.Resolution.RegressionTestRef != "t/ref" || out.Resolution.Guardrail == nil || out.Resolution.Guardrail.Type != "doc" {
		t.Errorf("resolution refs lost: %+v", out.Resolution)
	}
}

// TestLegacyStateLoadsWithNewFieldsAbsent covers "Legacy state loads unchanged" and
// "Old session resumes under new gates": a pre-change state.json (no new keys)
// recovers with the new fields zero, and a confirm attempt against its
// prediction-less probe is rejected with the standard repair instruction rather
// than corrupting the stored session.
func TestLegacyStateLoadsWithNewFieldsAbsent(t *testing.T) {
	root := t.TempDir()
	id := "legacy1"
	dir := filepath.Join(root, "sessions", id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-change open session: a board, a prediction-less probe, and a closed
	// reproduced run — none carrying any harden-detective-gates field.
	legacy := `{
  "id": "legacy1",
  "description": "legacy bug",
  "cwd": "/repo/legacy",
  "created_at": "2024-01-02T03:04:05Z",
  "hypotheses": [
    {"id": "h1", "statement": "suspect one", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"},
    {"id": "h2", "statement": "suspect two", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"},
    {"id": "h3", "statement": "suspect three", "status": "active", "created_at": "2024-01-02T03:04:05Z", "updated_at": "2024-01-02T03:04:05Z"}
  ],
  "probes": [
    {"id": "p1", "file": "old.go", "line": 7, "hypothesis_id": "h1", "removed": false, "created_at": "2024-01-02T03:04:05Z"}
  ],
  "runs": [
    {"id": "r1", "started_at": "2024-01-02T03:04:05Z", "closed_at": "2024-01-02T03:05:05Z", "verdict": "reproduced"}
  ]
}`
	if err := os.WriteFile(filepath.Join(dir, "state.json"), []byte(legacy), 0o644); err != nil {
		t.Fatalf("write legacy state: %v", err)
	}

	s, err := New(WithRoot(root), WithCommitResolver(func(string) string { return "" }))
	if err != nil {
		t.Fatalf("New over legacy root: %v", err)
	}
	got, err := s.GetSession(id)
	if err != nil {
		t.Fatalf("GetSession legacy: %v", err)
	}
	if got.Commit != "" || len(got.Probes) != 1 || got.Probes[0].ExpectedIfTrue != "" || got.Probes[0].ExpectedIfFalse != "" {
		t.Errorf("legacy session should load with new fields absent: %+v", got)
	}

	// Confirming h1 against the legacy prediction-less probe is rejected with the
	// standard repair instruction — not a corruption or migration of the stored
	// session (the confirm gate demands a predicted probe).
	_, err = s.UpdateHypothesisWithEvidence(id, "h1", HypothesisConfirmed, "n", "p1", "r1")
	if !errors.Is(err, ErrCitedProbeUnpredicted) {
		t.Fatalf("want ErrCitedProbeUnpredicted for a legacy prediction-less probe, got %v", err)
	}
	after, _ := s.GetSession(id)
	if after.Hypotheses[0].Status != HypothesisActive {
		t.Errorf("rejected confirm must leave the stored hypothesis unchanged: %+v", after.Hypotheses[0])
	}

	// The legacy state file must not have been migrated or rewritten by loading it or
	// by the rejected transition: the raw record still shows h1 active and p1 with no
	// predictions (no silent upgrade of the persisted shape).
	raw, err := s.readState(id)
	if err != nil {
		t.Fatalf("readState: %v", err)
	}
	if raw.Hypotheses[0].Status != HypothesisActive || raw.Probes[0].ExpectedIfTrue != "" || raw.Probes[0].ExpectedIfFalse != "" {
		t.Errorf("legacy state was rewritten/migrated on load: %+v", raw)
	}
}

// --- Hypothesis status enum (dogfood regression) ---

// TestInvalidHypothesisStatusRejected is the regression test for the release-
// dogfood blocker: a status outside the lifecycle enum ("", "garbage") used to
// skip the kill/confirm citation gates entirely and still be written to the
// board. Any unknown status must be rejected before mutation.
func TestInvalidHypothesisStatusRejected(t *testing.T) {
	for _, status := range []HypothesisStatus{"", "garbage", "KILLED"} {
		t.Run("status="+string(status), func(t *testing.T) {
			s := newTestStore(t)
			sess, _, _ := s.CreateSession("", "bug", "/repo")
			s.SetHypotheses(sess.ID, []string{"a", "b", "c"})

			_, err := s.UpdateHypothesisWithEvidence(sess.ID, "h1", status, "n", "", "")
			if !errors.Is(err, ErrInvalidHypothesisStatus) {
				t.Fatalf("want ErrInvalidHypothesisStatus, got %v", err)
			}
			if !strings.Contains(err.Error(), "active, killed, confirmed") {
				t.Errorf("error should name the allowed statuses: %v", err)
			}
			got, _ := s.GetSession(sess.ID)
			if got.Hypotheses[0].Status != HypothesisActive || got.Hypotheses[0].Note != "" {
				t.Errorf("board must be unchanged on rejection: %+v", got.Hypotheses[0])
			}
		})
	}
}

// --- Prevention references are optional (dogfood clarification) ---

// TestSolvedCloseWithoutRefsSucceeds pins regression_test_ref and guardrail as
// OPTIONAL: a solved close carrying only the three required fields is valid.
// Added after the release dogfood, where a trial wrongly inferred the refs were
// mandatory; this guards against them ever silently becoming so.
func TestSolvedCloseWithoutRefsSucceeds(t *testing.T) {
	s := newTestStore(t)
	sess, _, _ := s.CreateSession("", "bug", "/repo")
	s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
	confirmH1(t, s, sess.ID)

	_, err := s.CloseSessionWithResolution(sess.ID, &Resolution{
		RootCause: "root", FixSummary: "fix", ConfirmedHypothesisID: "h1",
	})
	if err != nil {
		t.Fatalf("refs-free solved close should succeed: %v", err)
	}
	got, _ := s.GetSession(sess.ID)
	if got.Resolution == nil || got.Resolution.RegressionTestRef != "" || got.Resolution.Guardrail != nil {
		t.Errorf("resolution should persist with empty refs: %+v", got.Resolution)
	}
}

// --- Probe id + resolution text (restart-on-upgrade dogfood findings) ---

// TestProbeIDRequired pins the raw-API gap the dogfood found: a blank probe id
// used to persist and zero-fill as ("", 0) in every await summary. The store now
// rejects it; the MCP tool always supplies an id, so only raw callers ever see this.
func TestProbeIDRequired(t *testing.T) {
	for _, id := range []string{"", "   "} {
		t.Run("id="+id, func(t *testing.T) {
			s := newTestStore(t)
			sess, _, _ := s.CreateSession("", "bug", "/repo")
			s.SetHypotheses(sess.ID, []string{"a", "b", "c"})

			_, err := s.RegisterProbe(sess.ID, Probe{ID: id, File: "f.go", Line: 1, HypothesisID: "h1"})
			if !errors.Is(err, ErrProbeIDRequired) {
				t.Fatalf("want ErrProbeIDRequired, got %v", err)
			}
			got, _ := s.GetSession(sess.ID)
			if len(got.Probes) != 0 {
				t.Errorf("blank-id probe must not persist: %+v", got.Probes)
			}
		})
	}
}

// TestResolutionTextGate pins resolution fields as single-line plain text: a past
// close persisted multi-line tool-call fragments into a root cause (dogfood find),
// polluting recall and the board. Control characters are rejected per field; the
// rejection leaves the session open.
func TestResolutionTextGate(t *testing.T) {
	clean := func() *Resolution {
		return &Resolution{RootCause: "root", FixSummary: "fix", ConfirmedHypothesisID: "h1"}
	}
	cases := []struct {
		name   string
		mutate func(*Resolution)
	}{
		{"newline in root_cause", func(r *Resolution) { r.RootCause = "root\n</root_cause>" }},
		{"tab in fix_summary", func(r *Resolution) { r.FixSummary = "fix\tsummary" }},
		{"carriage return in regression_test_ref", func(r *Resolution) { r.RegressionTestRef = "Test\rName" }},
		{"escape in guardrail ref", func(r *Resolution) { r.Guardrail = &Guardrail{Type: "lint", Ref: "rule\x1b[0m"} }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := newTestStore(t)
			sess, _, _ := s.CreateSession("", "bug", "/repo")
			s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
			confirmH1(t, s, sess.ID)

			res := clean()
			tc.mutate(res)
			_, err := s.CloseSessionWithResolution(sess.ID, res)
			if !errors.Is(err, ErrInvalidResolutionText) {
				t.Fatalf("want ErrInvalidResolutionText, got %v", err)
			}
			if !strings.Contains(err.Error(), "single-line") {
				t.Errorf("error should state the single-line contract: %v", err)
			}
			got, _ := s.GetSession(sess.ID)
			if got.ClosedAt != nil {
				t.Errorf("rejected resolution text must leave the session open: %v", got.ClosedAt)
			}
		})
	}

	t.Run("clean single-line close passes", func(t *testing.T) {
		s := newTestStore(t)
		sess, _, _ := s.CreateSession("", "bug", "/repo")
		s.SetHypotheses(sess.ID, []string{"a", "b", "c"})
		confirmH1(t, s, sess.ID)
		if _, err := s.CloseSessionWithResolution(sess.ID, clean()); err != nil {
			t.Fatalf("clean resolution should close: %v", err)
		}
	})
}
